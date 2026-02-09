package channel

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stellarlinkco/myclaw/internal/bus"
	"github.com/stellarlinkco/myclaw/internal/config"
)

type mockWeComSend struct {
	ResponseURL string
	Message     bus.OutboundMessage
}

type mockWeComClient struct {
	sent []mockWeComSend
	err  error
}

func (m *mockWeComClient) SendMessage(ctx context.Context, responseURL string, msg bus.OutboundMessage) error {
	m.sent = append(m.sent, mockWeComSend{ResponseURL: responseURL, Message: msg})
	return m.err
}

func (m *mockWeComClient) Close() {}

func mockWeComClientFactory(client *mockWeComClient) WeComClientFactory {
	return func(cfg config.WeComConfig) WeComClient {
		return client
	}
}

func TestNewWeComChannel_Valid(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, err := NewWeComChannel(config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
	}, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch.Name() != "wecom" {
		t.Errorf("Name = %q, want wecom", ch.Name())
	}
}

func TestNewWeComChannel_MissingRequiredConfig(t *testing.T) {
	b := bus.NewMessageBus(10)
	_, err := NewWeComChannel(config.WeComConfig{}, b)
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestWeComChannel_Send_NilClient(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewWeComChannelWithFactory(config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
	}, b, nil)

	err := ch.Send(bus.OutboundMessage{ChatID: "zhangsan", Content: "hello"})
	if err == nil {
		t.Fatal("expected error when client is nil")
	}
}

func TestWeComCallback_VerifyURL_OK(t *testing.T) {
	ch, _ := newTestWeComChannel(t, config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
		AllowFrom:      []string{"zhangsan"},
	})

	timestamp := "1739000000"
	nonce := "nonce-1"
	echostr := testWeComEncrypt(t, ch.cfg.EncodingAESKey, ch.receiveID, "hello-challenge")
	signature := testWeComSignature(ch.cfg.Token, timestamp, nonce, echostr)

	req := httptest.NewRequest(http.MethodGet, "/wecom/bot", nil)
	q := req.URL.Query()
	q.Set("msg_signature", signature)
	q.Set("timestamp", timestamp)
	q.Set("nonce", nonce)
	q.Set("echostr", echostr)
	req.URL.RawQuery = q.Encode()

	w := httptest.NewRecorder()
	ch.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "hello-challenge" {
		t.Fatalf("body = %q, want hello-challenge", w.Body.String())
	}
}

func TestWeComCallback_VerifyURL_BadSignature(t *testing.T) {
	ch, _ := newTestWeComChannel(t, config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
	})

	req := httptest.NewRequest(http.MethodGet, "/wecom/bot?msg_signature=bad&timestamp=1&nonce=2&echostr=abc", nil)
	w := httptest.NewRecorder()

	ch.handleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestWeComCallback_ReceiveTextMessage_OK(t *testing.T) {
	ch, b := newTestWeComChannel(t, config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
		AllowFrom:      []string{"zhangsan"},
	})

	timestamp := "1739000001"
	nonce := "nonce-2"
	plaintext := `{"msgid":"10001","aibotid":"AIBOTID","chattype":"single","from":{"userid":"zhangsan"},"response_url":"https://example.com/resp","msgtype":"text","text":{"content":"你好，myclaw"}}`
	encrypt := testWeComEncrypt(t, ch.cfg.EncodingAESKey, ch.receiveID, plaintext)
	signature := testWeComSignature(ch.cfg.Token, timestamp, nonce, encrypt)

	body := testWeComEncryptedRequestBody(t, encrypt)
	req := httptest.NewRequest(http.MethodPost, "/wecom/bot", strings.NewReader(body))
	q := req.URL.Query()
	q.Set("msg_signature", signature)
	q.Set("timestamp", timestamp)
	q.Set("nonce", nonce)
	req.URL.RawQuery = q.Encode()
	w := httptest.NewRecorder()

	ch.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var reply weComReplyEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode callback response: %v", err)
	}
	if reply.Encrypt == "" {
		t.Fatal("reply encrypt should not be empty")
	}
	if reply.MsgSignature == "" {
		t.Fatal("reply msgsignature should not be empty")
	}
	if reply.MsgSignature != testWeComSignature(ch.cfg.Token, reply.Timestamp, reply.Nonce, reply.Encrypt) {
		t.Fatal("reply msgsignature mismatch")
	}

	ackPlain := testWeComDecrypt(t, ch.cfg.EncodingAESKey, ch.receiveID, reply.Encrypt)
	if strings.TrimSpace(ackPlain) != `"success"` {
		t.Fatalf("ack plaintext = %q, want %q", ackPlain, `"success"`)
	}

	select {
	case msg := <-b.Inbound:
		if msg.Channel != "wecom" {
			t.Errorf("channel = %q, want wecom", msg.Channel)
		}
		if msg.SenderID != "zhangsan" {
			t.Errorf("senderID = %q, want zhangsan", msg.SenderID)
		}
		if msg.ChatID != "zhangsan" {
			t.Errorf("chatID = %q, want zhangsan", msg.ChatID)
		}
		if msg.Content != "你好，myclaw" {
			t.Errorf("content = %q, want 你好，myclaw", msg.Content)
		}
		if msg.Metadata["response_url"] != "https://example.com/resp" {
			t.Errorf("response_url = %v, want https://example.com/resp", msg.Metadata["response_url"])
		}
	case <-time.After(time.Second):
		t.Fatal("expected inbound message")
	}
}

func TestWeComCallback_AllowAllWhenAllowListEmpty(t *testing.T) {
	ch, b := newTestWeComChannel(t, config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
		AllowFrom:      []string{},
	})

	timestamp := "1739000002"
	nonce := "nonce-3"
	plaintext := `{"msgid":"10002","aibotid":"AIBOTID","chattype":"single","from":{"userid":"lisi"},"response_url":"https://example.com/resp","msgtype":"text","text":{"content":"hello"}}`
	encrypt := testWeComEncrypt(t, ch.cfg.EncodingAESKey, ch.receiveID, plaintext)
	signature := testWeComSignature(ch.cfg.Token, timestamp, nonce, encrypt)
	body := testWeComEncryptedRequestBody(t, encrypt)

	req := httptest.NewRequest(http.MethodPost, "/wecom/bot", strings.NewReader(body))
	q := req.URL.Query()
	q.Set("msg_signature", signature)
	q.Set("timestamp", timestamp)
	q.Set("nonce", nonce)
	req.URL.RawQuery = q.Encode()
	w := httptest.NewRecorder()

	ch.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	select {
	case msg := <-b.Inbound:
		if msg.Content != "hello" {
			t.Errorf("Content = %q, want %q", msg.Content, "hello")
		}
		if msg.SenderID != "lisi" {
			t.Errorf("SenderID = %q, want %q", msg.SenderID, "lisi")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("should allow all senders when allowFrom is empty")
	}
}

func TestWeComCallback_DuplicateMsgID_Dropped(t *testing.T) {
	ch, b := newTestWeComChannel(t, config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
		AllowFrom:      []string{"zhangsan"},
	})

	timestamp := "1739000003"
	nonce := "nonce-4"
	plaintext := `{"msgid":"20001","aibotid":"AIBOTID","chattype":"single","from":{"userid":"zhangsan"},"response_url":"https://example.com/resp","msgtype":"text","text":{"content":"dup"}}`
	encrypt := testWeComEncrypt(t, ch.cfg.EncodingAESKey, ch.receiveID, plaintext)
	signature := testWeComSignature(ch.cfg.Token, timestamp, nonce, encrypt)
	body := testWeComEncryptedRequestBody(t, encrypt)

	post := func() {
		req := httptest.NewRequest(http.MethodPost, "/wecom/bot", strings.NewReader(body))
		q := req.URL.Query()
		q.Set("msg_signature", signature)
		q.Set("timestamp", timestamp)
		q.Set("nonce", nonce)
		req.URL.RawQuery = q.Encode()
		w := httptest.NewRecorder()
		ch.handleCallback(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}

	post()
	post()
	time.Sleep(50 * time.Millisecond)

	count := 0
	for {
		select {
		case <-b.Inbound:
			count++
		default:
			if count != 1 {
				t.Fatalf("inbound count = %d, want 1", count)
			}
			return
		}
	}
}

func TestWeComChannel_Send_Success(t *testing.T) {
	b := bus.NewMessageBus(10)
	mock := &mockWeComClient{}

	ch, err := NewWeComChannelWithFactory(config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		AllowFrom:      []string{"zhangsan"},
	}, b, mockWeComClientFactory(mock))
	if err != nil {
		t.Fatalf("new channel error: %v", err)
	}
	ch.client = mock
	ch.replyCache.Set("zhangsan", "https://example.com/response-url")

	err = ch.Send(bus.OutboundMessage{ChatID: "zhangsan", Content: "pong"})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}

	if len(mock.sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(mock.sent))
	}
	if mock.sent[0].ResponseURL != "https://example.com/response-url" {
		t.Errorf("response_url = %q, want https://example.com/response-url", mock.sent[0].ResponseURL)
	}
	if mock.sent[0].Message.ChatID != "zhangsan" {
		t.Errorf("chatID = %q, want zhangsan", mock.sent[0].Message.ChatID)
	}
	if mock.sent[0].Message.Content != "pong" {
		t.Errorf("content = %q, want pong", mock.sent[0].Message.Content)
	}
}

func TestWeComChannel_Send_ResponseURLMissing(t *testing.T) {
	b := bus.NewMessageBus(10)
	mock := &mockWeComClient{}

	ch, err := NewWeComChannelWithFactory(config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		AllowFrom:      []string{"zhangsan"},
	}, b, mockWeComClientFactory(mock))
	if err != nil {
		t.Fatalf("new channel error: %v", err)
	}
	ch.client = mock

	err = ch.Send(bus.OutboundMessage{ChatID: "zhangsan", Content: "pong"})
	if err == nil {
		t.Fatal("expected response_url missing error")
	}
	if !strings.Contains(err.Error(), "response_url") {
		t.Fatalf("error = %v, want response_url hint", err)
	}
}

func TestChannelManager_WeComEnabled_MissingConfig(t *testing.T) {
	b := bus.NewMessageBus(10)
	_, err := NewChannelManager(config.ChannelsConfig{
		WeCom: config.WeComConfig{Enabled: true},
	}, b)
	if err == nil {
		t.Fatal("expected error for missing wecom required config")
	}
}

func TestChannelManager_WeComEnabled(t *testing.T) {
	b := bus.NewMessageBus(10)
	m, err := NewChannelManager(config.ChannelsConfig{
		WeCom: config.WeComConfig{
			Enabled:        true,
			Token:          "verify-token",
			EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
			AllowFrom:      []string{"zhangsan"},
		},
	}, b)
	if err != nil {
		t.Fatalf("new channel manager error: %v", err)
	}

	found := false
	for _, name := range m.EnabledChannels() {
		if name == "wecom" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("enabled channels does not include wecom: %v", m.EnabledChannels())
	}
}

func newTestWeComChannel(t *testing.T, cfg config.WeComConfig) (*WeComChannel, *bus.MessageBus) {
	t.Helper()
	b := bus.NewMessageBus(10)
	mock := &mockWeComClient{}
	ch, err := NewWeComChannelWithFactory(cfg, b, mockWeComClientFactory(mock))
	if err != nil {
		t.Fatalf("new wecom channel error: %v", err)
	}
	ch.client = mock
	return ch, b
}

func testWeComEncryptedRequestBody(t *testing.T, encrypt string) string {
	t.Helper()
	body := map[string]string{"encrypt": encrypt}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal encrypted body: %v", err)
	}
	return string(data)
}

func testWeComEncrypt(t *testing.T, encodingAESKey, receiveID, plaintext string) string {
	t.Helper()
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		t.Fatalf("decode aes key: %v", err)
	}
	if len(aesKey) != 32 {
		t.Fatalf("invalid aes key len: %d", len(aesKey))
	}

	random16 := []byte("0123456789abcdef")
	msg := []byte(plaintext)
	msgLen := make([]byte, 4)
	binary.BigEndian.PutUint32(msgLen, uint32(len(msg)))
	raw := append(append(append(random16, msgLen...), msg...), []byte(receiveID)...)

	padded := testPKCS7Pad(raw, 32)

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	iv := aesKey[:16]
	mode := cipher.NewCBCEncrypter(block, iv)
	cipherData := make([]byte, len(padded))
	mode.CryptBlocks(cipherData, padded)

	return base64.StdEncoding.EncodeToString(cipherData)
}

func testWeComDecrypt(t *testing.T, encodingAESKey, expectedReceiveID, encrypted string) string {
	t.Helper()
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		t.Fatalf("decode aes key: %v", err)
	}
	if len(aesKey) != 32 {
		t.Fatalf("invalid aes key len: %d", len(aesKey))
	}

	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		t.Fatalf("decode encrypted body: %v", err)
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	iv := aesKey[:16]
	mode := cipher.NewCBCDecrypter(block, iv)
	plain := make([]byte, len(raw))
	mode.CryptBlocks(plain, raw)

	plain, err = pkcs7Unpad(plain, 32)
	if err != nil {
		t.Fatalf("pkcs7 unpad: %v", err)
	}

	if len(plain) < 20 {
		t.Fatalf("plaintext too short: %d", len(plain))
	}
	msgLen := int(binary.BigEndian.Uint32(plain[16:20]))
	if msgLen < 0 || 20+msgLen > len(plain) {
		t.Fatalf("invalid msg length: %d", msgLen)
	}

	msg := string(plain[20 : 20+msgLen])
	receiveID := string(plain[20+msgLen:])
	if expectedReceiveID != "" && receiveID != expectedReceiveID {
		t.Fatalf("receiveID mismatch: got %q want %q", receiveID, expectedReceiveID)
	}

	return msg
}

func testPKCS7Pad(src []byte, blockSize int) []byte {
	padding := blockSize - len(src)%blockSize
	if padding == 0 {
		padding = blockSize
	}
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(src, padText...)
}

func testWeComSignature(token, timestamp, nonce, data string) string {
	items := []string{token, timestamp, nonce, data}
	sort.Strings(items)
	s := strings.Join(items, "")
	sum := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", sum)
}

func TestWeComClient_Send_IntegrationShape(t *testing.T) {
	sendCalls := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reply" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		sendCalls++
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid send payload json: %v", err)
		}
		if payload["msgtype"] != "markdown" {
			t.Errorf("msgtype = %v, want markdown", payload["msgtype"])
		}
		md := payload["markdown"].(map[string]any)
		if md["content"] != "hello from test" {
			t.Errorf("content = %v, want hello from test", md["content"])
		}
		io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ts.Close()

	client := &defaultWeComClient{
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}

	err := client.SendMessage(context.Background(), ts.URL+"/reply", bus.OutboundMessage{ChatID: "zhangsan", Content: "hello from test"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	if sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", sendCalls)
	}
}

func TestWeComClient_Send_TruncateLongContent(t *testing.T) {
	sendCalls := 0
	var receivedContent string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendCalls++
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid send payload json: %v", err)
		}
		md := payload["markdown"].(map[string]any)
		receivedContent = md["content"].(string)
		io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ts.Close()

	client := &defaultWeComClient{
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}

	content := strings.Repeat("A", 25000)
	err := client.SendMessage(context.Background(), ts.URL, bus.OutboundMessage{ChatID: "zhangsan", Content: content})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	if sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1 (response_url is single-use)", sendCalls)
	}
	if len([]byte(receivedContent)) > 20480 {
		t.Fatalf("content bytes = %d, want <= 20480", len([]byte(receivedContent)))
	}
}

func TestWeComClient_Send_RetryTransientErrcode(t *testing.T) {
	sendCalls := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendCalls++
		if sendCalls == 1 {
			io.WriteString(w, `{"errcode":-1,"errmsg":"system busy"}`)
			return
		}
		io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ts.Close()

	client := &defaultWeComClient{
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}

	err := client.SendMessage(context.Background(), ts.URL, bus.OutboundMessage{ChatID: "zhangsan", Content: "retry me"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if sendCalls != 2 {
		t.Fatalf("send calls = %d, want 2", sendCalls)
	}
}

func TestWeComClient_Send_NoRetryOnPayloadErrcode(t *testing.T) {
	sendCalls := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendCalls++
		io.WriteString(w, `{"errcode":44004,"errmsg":"content size out of limit"}`)
	}))
	defer ts.Close()

	client := &defaultWeComClient{
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}

	err := client.SendMessage(context.Background(), ts.URL, bus.OutboundMessage{ChatID: "zhangsan", Content: "payload error"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "44004") {
		t.Fatalf("error = %v, want errcode 44004", err)
	}
	if sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", sendCalls)
	}
}
