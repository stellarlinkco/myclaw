package channel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stellarlinkco/myclaw/internal/bus"
	"github.com/stellarlinkco/myclaw/internal/channel/telegram"
	"github.com/stellarlinkco/myclaw/internal/config"
)

const fakeToken = "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefgh"

// mockCaller implements ta.Caller for testing telego bot methods.
type mockCaller struct {
	responses       map[string]*ta.Response // method name -> response
	calls           []mockCall
	callErr         error
	methodErrSeq    map[string][]error
	methodCallCount map[string]int
}

type mockCall struct {
	URL  string
	Data *ta.RequestData
}

func newMockCaller() *mockCaller {
	return &mockCaller{
		responses: map[string]*ta.Response{
			// Default: all methods succeed with minimal valid JSON
			"getMe":              {Ok: true, Result: []byte(`{"id":1,"is_bot":true,"first_name":"Test","username":"testbot"}`)},
			"sendMessage":        {Ok: true, Result: []byte(`{"message_id":1,"date":0,"chat":{"id":123,"type":"private"}}`)},
			"editMessageText":    {Ok: true, Result: []byte(`{"message_id":1,"date":0,"chat":{"id":123,"type":"private"}}`)},
			"setMessageReaction": {Ok: true, Result: []byte(`true`)},
			"sendChatAction":     {Ok: true, Result: []byte(`true`)},
			"getFile":            {Ok: true, Result: []byte(`{"file_id":"f1","file_path":"photos/test.jpg"}`)},
			"getUpdates":         {Ok: true, Result: []byte(`[]`)},
		},
		methodCallCount: map[string]int{},
	}
}

func (m *mockCaller) Call(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
	m.calls = append(m.calls, mockCall{URL: url, Data: data})
	if m.callErr != nil {
		return nil, m.callErr
	}
	// Extract method name from URL (last path segment)
	parts := strings.Split(url, "/")
	method := parts[len(parts)-1]
	if seq := m.methodErrSeq[method]; len(seq) > 0 {
		idx := m.methodCallCount[method]
		m.methodCallCount[method] = idx + 1
		if idx < len(seq) && seq[idx] != nil {
			return nil, seq[idx]
		}
	}
	if resp, ok := m.responses[method]; ok {
		return resp, nil
	}
	return &ta.Response{Ok: true, Result: []byte(`true`)}, nil
}

func newTestBot(t *testing.T, caller *mockCaller) *telego.Bot {
	t.Helper()
	bot, err := telego.NewBot(fakeToken, telego.WithAPICaller(caller))
	if err != nil {
		t.Fatalf("newTestBot: %v", err)
	}
	return bot
}

func newTestChannel(t *testing.T, cfg config.TelegramConfig) (*TelegramChannel, *mockCaller) {
	t.Helper()
	b := bus.NewMessageBus(10)
	if cfg.Token == "" {
		cfg.Token = fakeToken
	}
	ch, err := NewTelegramChannel(cfg, b)
	if err != nil {
		t.Fatalf("newTestChannel: %v", err)
	}
	caller := newMockCaller()
	bot := newTestBot(t, caller)
	ch.bot = bot
	return ch, caller
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// === Base Channel Tests ===

func TestBaseChannel_Name(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch := NewBaseChannel("test", b, nil)
	if ch.Name() != "test" {
		t.Errorf("Name = %q, want test", ch.Name())
	}
}

func TestBaseChannel_IsAllowed_NoFilter(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch := NewBaseChannel("test", b, nil)
	if !ch.IsAllowed("anyone") {
		t.Error("should allow anyone when allowFrom is empty")
	}
}

func TestBaseChannel_IsAllowed_WithFilter(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch := NewBaseChannel("test", b, []string{"user1", "user2"})
	if !ch.IsAllowed("user1") {
		t.Error("should allow user1")
	}
	if !ch.IsAllowed("user2") {
		t.Error("should allow user2")
	}
	if ch.IsAllowed("user3") {
		t.Error("should reject user3")
	}
}

// === Telegram Channel Constructor Tests ===
func TestNewTelegramChannel_NoToken(t *testing.T) {
	b := bus.NewMessageBus(10)
	_, err := NewTelegramChannel(config.TelegramConfig{}, b)
	if err == nil {
		t.Error("expected error for empty token")
	}
}
func TestNewTelegramChannel_Valid(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, err := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch.Name() != "telegram" {
		t.Errorf("Name = %q, want telegram", ch.Name())
	}
}

// === toTelegramHTML Tests ===
func TestToTelegramHTML(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"**bold**", "<b>bold</b>"},
		{"`code`", "<code>code</code>"},
		{"a & b", "a &amp; b"},
		{"<tag>", "&lt;tag&gt;"},
	}
	for _, tt := range tests {
		got := telegram.ToTelegramHTML(tt.input)
		if got != tt.want {
			t.Errorf("telegram.ToTelegramHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
func TestToTelegramHTML_CodeBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"code block with language", "```go\nfunc main() {}\n```", "<pre>func main() {}\n</pre>"},
		{"code block without language", "```\ncode here\n```", "<pre>\ncode here\n</pre>"},
		{"italic text", "*italic*", "<i>italic</i>"},
		{"mixed bold and italic", "**bold** and *italic*", "<b>bold</b> and <i>italic</i>"},
		{"unclosed code block", "```code", "```code"},
		{"unclosed inline code", "`code", "`code"},
		{"unclosed bold", "**bold", "**bold"},
		{"unclosed italic", "*italic", "*italic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := telegram.ToTelegramHTML(tt.input)
			if got != tt.want {
				t.Errorf("telegram.ToTelegramHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// === Channel Manager Tests ===
func TestChannelManager_Empty(t *testing.T) {
	b := bus.NewMessageBus(10)
	m, err := NewChannelManager(config.ChannelsConfig{}, b)
	if err != nil {
		t.Fatalf("NewChannelManager error: %v", err)
	}
	if len(m.EnabledChannels()) != 0 {
		t.Errorf("expected 0 enabled channels, got %d", len(m.EnabledChannels()))
	}
}

// === Mock Channel for Manager Tests ===
type mockChannel struct {
	name     string
	started  bool
	stopped  bool
	startErr error
	stopErr  error
	sentMsgs []bus.OutboundMessage
}

func (m *mockChannel) Name() string { return m.name }
func (m *mockChannel) Start(ctx context.Context) error {
	m.started = true
	return m.startErr
}
func (m *mockChannel) Stop() error {
	m.stopped = true
	return m.stopErr
}
func (m *mockChannel) Send(msg bus.OutboundMessage) error {
	m.sentMsgs = append(m.sentMsgs, msg)
	return nil
}
func TestChannelManager_WithMockChannel(t *testing.T) {
	b := bus.NewMessageBus(10)
	mock := &mockChannel{name: "mock"}
	m := &ChannelManager{
		channels: map[string]Channel{"mock": mock},
		bus:      b,
	}
	ctx := context.Background()
	if err := m.StartAll(ctx); err != nil {
		t.Errorf("StartAll error: %v", err)
	}
	if !mock.started {
		t.Error("mock channel should be started")
	}
	channels := m.EnabledChannels()
	if len(channels) != 1 || channels[0] != "mock" {
		t.Errorf("EnabledChannels = %v, want [mock]", channels)
	}
	if err := m.StopAll(); err != nil {
		t.Errorf("StopAll error: %v", err)
	}
	if !mock.stopped {
		t.Error("mock channel should be stopped")
	}
}
func TestChannelManager_StartAll_Empty(t *testing.T) {
	b := bus.NewMessageBus(10)
	m, _ := NewChannelManager(config.ChannelsConfig{}, b)
	ctx := context.Background()
	if err := m.StartAll(ctx); err != nil {
		t.Errorf("StartAll error: %v", err)
	}
}
func TestChannelManager_StopAll_Empty(t *testing.T) {
	b := bus.NewMessageBus(10)
	m, _ := NewChannelManager(config.ChannelsConfig{}, b)
	if err := m.StopAll(); err != nil {
		t.Errorf("StopAll error: %v", err)
	}
}
func TestChannelManager_StartAll_Error(t *testing.T) {
	b := bus.NewMessageBus(10)
	mock := &mockChannel{name: "mock", startErr: fmt.Errorf("start failed")}
	m := &ChannelManager{
		channels: map[string]Channel{"mock": mock},
		bus:      b,
	}
	ctx := context.Background()
	err := m.StartAll(ctx)
	if err == nil {
		t.Error("expected error from StartAll")
	}
}
func TestChannelManager_StopAll_Error(t *testing.T) {
	b := bus.NewMessageBus(10)
	mock := &mockChannel{name: "mock", stopErr: fmt.Errorf("stop failed")}
	m := &ChannelManager{
		channels: map[string]Channel{"mock": mock},
		bus:      b,
	}
	if err := m.StopAll(); err != nil {
		t.Errorf("StopAll should not return error: %v", err)
	}
}

// === Telegram Channel Tests ===
func TestTelegramChannel_Stop_NotStarted(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	err := ch.Stop()
	if err != nil {
		t.Errorf("Stop error: %v", err)
	}
}
func TestTelegramChannel_Send_NilBot(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	err := ch.Send(bus.OutboundMessage{ChatID: "123", Content: "test"})
	if err == nil {
		t.Error("expected error when bot is nil")
	}
}
func TestTelegramChannel_WithProxy(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, err := NewTelegramChannel(config.TelegramConfig{
		Token: fakeToken,
		Proxy: "http://proxy.local:8080",
	}, b)
	if err != nil {
		t.Fatalf("NewTelegramChannel error: %v", err)
	}
	if ch.proxy != "http://proxy.local:8080" {
		t.Errorf("proxy = %q, want http://proxy.local:8080", ch.proxy)
	}
}
func TestTelegramChannel_Send_InvalidChatID(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	err := ch.Send(bus.OutboundMessage{ChatID: "not-a-number", Content: "test"})
	if err == nil {
		t.Error("expected error for invalid chat ID")
	}
}
func TestTelegramChannel_Send_Success(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{})
	err := ch.Send(bus.OutboundMessage{ChatID: "123", Content: "hello"})
	if err != nil {
		t.Errorf("Send error: %v", err)
	}
	if len(caller.calls) == 0 {
		t.Error("expected at least one API call")
	}
}
func TestTelegramChannel_Send_LongMessage(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{})
	longContent := ""
	for i := 0; i < 100; i++ {
		longContent += "This is a long line of text that will be repeated.\n"
	}
	err := ch.Send(bus.OutboundMessage{ChatID: "123", Content: longContent})
	if err != nil {
		t.Errorf("Send error: %v", err)
	}
	// Count sendMessage calls
	sendCount := 0
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			sendCount++
		}
	}
	if sendCount < 2 {
		t.Errorf("expected multiple sent messages for long content, got %d", sendCount)
	}
}
func TestTelegramChannel_Send_LongMessageNoNewline(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{})
	longContent := strings.Repeat("x", 5000)
	err := ch.Send(bus.OutboundMessage{ChatID: "123", Content: longContent})
	if err != nil {
		t.Errorf("Send error: %v", err)
	}
	sendCount := 0
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			sendCount++
		}
	}
	if sendCount < 2 {
		t.Errorf("expected multiple messages, got %d", sendCount)
	}
}
func TestTelegramChannel_Send_HTMLError_Retry(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	retryCaller := &retrySendCaller{inner: newMockCaller(), failFirst: true}
	bot, _ := telego.NewBot(fakeToken, telego.WithAPICaller(retryCaller))
	ch.bot = bot
	err := ch.Send(bus.OutboundMessage{ChatID: "123", Content: "test"})
	if err != nil {
		t.Errorf("Send should succeed after retry: %v", err)
	}
}

// retrySendCaller fails the first sendMessage call, succeeds on subsequent ones.
type retrySendCaller struct {
	inner     *mockCaller
	failFirst bool
	callCount int
}

func (r *retrySendCaller) Call(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
	r.callCount++
	if r.failFirst && r.callCount == 1 && strings.HasSuffix(url, "/sendMessage") {
		return &ta.Response{Ok: false, Error: &ta.Error{Description: "HTML parse error", ErrorCode: 400}}, nil
	}
	return r.inner.Call(ctx, url, data)
}
func TestTelegramChannel_Send_BothFail(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{})
	caller.responses["sendMessage"] = &ta.Response{Ok: false, Error: &ta.Error{Description: "send failed", ErrorCode: 400}}
	err := ch.Send(bus.OutboundMessage{ChatID: "123", Content: "test"})
	if err == nil {
		t.Error("expected error when both sends fail")
	}
}

// === HandleMessage Tests ===
func TestTelegramChannel_HandleMessage_Allowed(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123, Username: "testuser"},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "hello",
		Date: 1234567890,
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if inbound.Content != "hello" {
			t.Errorf("content = %q, want hello", inbound.Content)
		}
		if inbound.SenderID != "123" {
			t.Errorf("senderID = %q, want 123", inbound.SenderID)
		}
		if inbound.ChatID != "456" {
			t.Errorf("chatID = %q, want 456", inbound.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}

func TestTelegramChannel_HandleMessage_Rejected(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{
		Token:     fakeToken,
		AllowFrom: []string{"999"},
	}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123, Username: "testuser"},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "hello",
	}
	ch.handleMessage(msg)
	select {
	case <-b.Inbound:
		t.Error("should not receive message from rejected user")
	default:
	}
}
func TestTelegramChannel_HandleMessage_EmptyText(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "",
	}
	ch.handleMessage(msg)
	select {
	case <-b.Inbound:
		t.Error("should not send message with empty content")
	default:
	}
}
func TestTelegramChannel_HandleMessage_Caption(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From:    &telego.User{ID: 123},
		Chat:    telego.Chat{ID: 456, Type: "private"},
		Text:    "",
		Caption: "image caption",
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if inbound.Content != "image caption" {
			t.Errorf("content = %q, want 'image caption'", inbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_Photo(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	photoData := []byte{0xff, 0xd8, 0xff, 0xd9}
	ch.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(photoData)),
			Header:     make(http.Header),
		}, nil
	})}
	msg := &telego.Message{
		From:    &telego.User{ID: 123},
		Chat:    telego.Chat{ID: 456, Type: "private"},
		Caption: "photo caption",
		Photo: []telego.PhotoSize{
			{FileID: "photo-small"},
			{FileID: "photo-large"},
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-ch.bus.Inbound:
		if inbound.Content != "photo caption" {
			t.Errorf("content = %q, want 'photo caption'", inbound.Content)
		}
		if len(inbound.ContentBlocks) != 1 {
			t.Fatalf("content blocks len = %d, want 1", len(inbound.ContentBlocks))
		}
		block := inbound.ContentBlocks[0]
		if block.Type != model.ContentBlockImage {
			t.Errorf("content block type = %q, want %q", block.Type, model.ContentBlockImage)
		}
		if block.Data != base64.StdEncoding.EncodeToString(photoData) {
			t.Errorf("content block data mismatch")
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_PhotoWithCaption(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	photoData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(photoData)
	}))
	defer downloadServer.Close()
	serverURL, err := url.Parse(downloadServer.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	ch.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clonedReq := req.Clone(req.Context())
		clonedReq.URL.Scheme = serverURL.Scheme
		clonedReq.URL.Host = serverURL.Host
		return transport.RoundTrip(clonedReq)
	})}
	msg := &telego.Message{
		From:    &telego.User{ID: 123},
		Chat:    telego.Chat{ID: 456, Type: "private"},
		Caption: "photo caption via server",
		Photo: []telego.PhotoSize{
			{FileID: "photo-small"},
			{FileID: "photo-large"},
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-ch.bus.Inbound:
		if inbound.Content != "photo caption via server" {
			t.Errorf("content = %q, want 'photo caption via server'", inbound.Content)
		}
		if len(inbound.ContentBlocks) != 1 {
			t.Fatalf("content blocks len = %d, want 1", len(inbound.ContentBlocks))
		}
		block := inbound.ContentBlocks[0]
		if block.Type != model.ContentBlockImage {
			t.Errorf("content block type = %q, want %q", block.Type, model.ContentBlockImage)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_Document(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	workspace := t.TempDir()
	ch.SetWorkspace(workspace)
	pdfData := []byte("%PDF-1.4\n")
	ch.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(pdfData)),
			Header:     make(http.Header),
		}, nil
	})}
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Document: &telego.Document{
			FileID:   "doc-1",
			MimeType: "application/pdf",
			FileName: "test.pdf",
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-ch.bus.Inbound:
		if !strings.Contains(inbound.Content, "[File saved to:") {
			t.Errorf("content = %q, want file path reference", inbound.Content)
		}
		if len(inbound.ContentBlocks) != 0 {
			t.Errorf("content blocks len = %d, want 0 (file saved to disk)", len(inbound.ContentBlocks))
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}

// === Reply Context Tests ===
func TestTelegramChannel_HandleMessage_ReplyContext(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123, Username: "testuser"},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "what does this mean?",
		Date: 1234567890,
		ReplyToMessage: &telego.Message{
			From: &telego.User{ID: 789, FirstName: "Alice", LastName: "B"},
			Text: "The quick brown fox jumps over the lazy dog",
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if !strings.Contains(inbound.Content, "[Replying to Alice B]") {
			t.Errorf("missing reply context header, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "The quick brown fox") {
			t.Errorf("missing replied-to text, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "what does this mean?") {
			t.Errorf("missing user text, got: %q", inbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_ReplyToPhoto(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "describe this image",
		Date: 1234567890,
		ReplyToMessage: &telego.Message{
			From:  &telego.User{ID: 789, FirstName: "Bob"},
			Photo: []telego.PhotoSize{{FileID: "photo-1"}},
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if !strings.Contains(inbound.Content, "[Replying to Bob]") {
			t.Errorf("missing reply header, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "[Photo]") {
			t.Errorf("missing photo indicator, got: %q", inbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_ExternalReply(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "reply ping",
		Date: 1234567890,
		ExternalReply: &telego.ExternalReplyInfo{
			Origin: &telego.MessageOriginChannel{
				Chat: telego.Chat{Title: "Tech News"},
			},
		},
		Quote: &telego.TextQuote{Text: "Breaking news content here"},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if !strings.Contains(inbound.Content, "[Replying to channel: Tech News]") {
			t.Errorf("missing external reply header, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "Breaking news content here") {
			t.Errorf("missing quote text, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "reply ping") {
			t.Errorf("missing user text, got: %q", inbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_ExternalReplyWithPhoto(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	photoData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(photoData)
	}))
	defer downloadServer.Close()
	serverURL, err := url.Parse(downloadServer.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	ch.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clonedReq := req.Clone(req.Context())
		clonedReq.URL.Scheme = serverURL.Scheme
		clonedReq.URL.Host = serverURL.Host
		return transport.RoundTrip(clonedReq)
	})}
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "what is this image about",
		Date: 1234567890,
		ExternalReply: &telego.ExternalReplyInfo{
			Origin: &telego.MessageOriginChannel{
				Chat: telego.Chat{Title: "Linux.do 热门话题"},
			},
			Photo: []telego.PhotoSize{
				{FileID: "ext-photo-small", Width: 76, Height: 998},
				{FileID: "ext-photo-large", Width: 760, Height: 9980},
			},
		},
		Quote: &telego.TextQuote{Text: "some quoted text"},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-ch.bus.Inbound:
		if !strings.Contains(inbound.Content, "[Replying to channel: Linux.do 热门话题]") {
			t.Errorf("missing external reply header, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "some quoted text") {
			t.Errorf("missing quote text, got: %q", inbound.Content)
		}
		if len(inbound.ContentBlocks) != 1 {
			t.Fatalf("expected 1 content block (photo), got %d", len(inbound.ContentBlocks))
		}
		if inbound.ContentBlocks[0].Type != model.ContentBlockImage {
			t.Errorf("block type = %q, want image", inbound.ContentBlocks[0].Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}

// === Forward Message Tests ===
func TestTelegramChannel_HandleMessage_ForwardWithText(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Text: "Check this out",
		Date: 1234567890,
		ForwardOrigin: &telego.MessageOriginUser{
			SenderUser: telego.User{FirstName: "Charlie", LastName: "D"},
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if !strings.Contains(inbound.Content, "[Forwarded from Charlie D]") {
			t.Errorf("missing forward label, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "Check this out") {
			t.Errorf("missing forwarded text, got: %q", inbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}
func TestTelegramChannel_HandleMessage_ForwardNoComment(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{Token: fakeToken}, b)
	msg := &telego.Message{
		From: &telego.User{ID: 123},
		Chat: telego.Chat{ID: 456, Type: "private"},
		Date: 1234567890,
		ForwardOrigin: &telego.MessageOriginChannel{
			Chat: telego.Chat{Title: "Tech News"},
		},
	}
	ch.handleMessage(msg)
	select {
	case inbound := <-b.Inbound:
		if !strings.Contains(inbound.Content, "[Forwarded from channel: Tech News]") {
			t.Errorf("missing forward label, got: %q", inbound.Content)
		}
		if !strings.Contains(inbound.Content, "Summarize or process") {
			t.Errorf("missing summarize hint, got: %q", inbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected inbound message")
	}
}

// === Media Group Tests ===
func TestTelegramChannel_HandleMessage_MediaGroup(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	photoData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(photoData)
	}))
	defer downloadServer.Close()
	serverURL, err := url.Parse(downloadServer.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	ch.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clonedReq := req.Clone(req.Context())
		clonedReq.URL.Scheme = serverURL.Scheme
		clonedReq.URL.Host = serverURL.Host
		return transport.RoundTrip(clonedReq)
	})}
	// Simulate 3 photos in a media group (Telegram sends each as separate Message).
	for i := 0; i < 3; i++ {
		msg := &telego.Message{
			From:         &telego.User{ID: 123},
			Chat:         telego.Chat{ID: 456, Type: "private"},
			Date:         1234567890,
			MediaGroupID: "mg-abc-123",
			Photo:        []telego.PhotoSize{{FileID: fmt.Sprintf("photo-%d", i)}},
		}
		if i == 0 {
			msg.Caption = "album caption"
		}
		ch.handleMessage(msg)
	}
	// Should produce exactly ONE inbound message after flush.
	select {
	case inbound := <-ch.bus.Inbound:
		if !strings.Contains(inbound.Content, "album caption") {
			t.Errorf("missing caption, got: %q", inbound.Content)
		}
		if len(inbound.ContentBlocks) != 3 {
			t.Fatalf("expected 3 content blocks (photos), got %d", len(inbound.ContentBlocks))
		}
		for i, block := range inbound.ContentBlocks {
			if block.Type != model.ContentBlockImage {
				t.Errorf("block[%d] type = %q, want image", i, block.Type)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected exactly one inbound message from media group")
	}
	// Verify no second message arrives.
	select {
	case extra := <-ch.bus.Inbound:
		t.Fatalf("unexpected second inbound message: %+v", extra)
	case <-time.After(300 * time.Millisecond):
		// Good — no duplicate.
	}
}

// === WeChat Image Test (unchanged, no tgbotapi dependency) ===
func TestWeComCallback_ImageMessage(t *testing.T) {
	imageData := []byte{0xff, 0xd8, 0xff, 0xd9}
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(imageData)
	}))
	defer imageServer.Close()
	ch, b := newTestWeComChannel(t, config.WeComConfig{
		Token:          "verify-token",
		EncodingAESKey: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		ReceiveID:      "recv-id-1",
		AllowFrom:      []string{"zhangsan"},
	})
	timestamp := "1739000200"
	nonce := "nonce-image"
	imageURL := imageServer.URL + "/image.jpg"
	plaintext := fmt.Sprintf(`{"msgid":"20001","aibotid":"AIBOTID","chattype":"single","from":{"userid":"zhangsan"},"response_url":"https://example.com/resp","msgtype":"image","image":{"url":"%s"}}`, imageURL)
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
	case inbound := <-b.Inbound:
		if inbound.Content != "[image]" {
			t.Errorf("content = %q, want [image]", inbound.Content)
		}
		if len(inbound.ContentBlocks) != 1 {
			t.Fatalf("content blocks len = %d, want 1", len(inbound.ContentBlocks))
		}
		block := inbound.ContentBlocks[0]
		if block.Type != model.ContentBlockImage {
			t.Errorf("content block type = %q, want %q", block.Type, model.ContentBlockImage)
		}
		if block.MediaType != "image/jpeg" {
			t.Errorf("media type = %q, want image/jpeg", block.MediaType)
		}
	case <-time.After(time.Second):
		t.Fatal("expected inbound message")
	}
}

// === InitBot and Start Tests ===
func TestTelegramChannel_InitBot_InvalidProxy(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, _ := NewTelegramChannel(config.TelegramConfig{
		Token: fakeToken,
		Proxy: "://invalid-url",
	}, b)
	err := ch.initBot()
	if err == nil {
		t.Error("expected error for invalid proxy URL")
	}
}

func TestTelegramChannel_RegisteredBotCommands(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{Token: fakeToken})
	ch.slashCommands = map[string]telegram.Command{
		"status": {Name: "status", Description: "Check bot status"},
		"help":   {Name: "help", Description: "  Show\navailable commands  "},
		"echo":   {Name: "echo"},
	}

	commands := ch.registeredBotCommands()
	if len(commands) != 4 {
		t.Fatalf("registeredBotCommands len = %d, want 4", len(commands))
	}

	gotNames := make([]string, 0, len(commands))
	gotDesc := make(map[string]string, len(commands))
	for _, cmd := range commands {
		gotNames = append(gotNames, cmd.Command)
		gotDesc[cmd.Command] = cmd.Description
	}

	if strings.Join(gotNames, ",") != "echo,help,new,status" {
		t.Fatalf("registered command names = %v, want [echo help new status]", gotNames)
	}
	if gotDesc["new"] != "Start a fresh session" {
		t.Fatalf("/new description = %q", gotDesc["new"])
	}
	if gotDesc["help"] != "Show available commands" {
		t.Fatalf("/help description = %q", gotDesc["help"])
	}
	if gotDesc["echo"] != "Run /echo" {
		t.Fatalf("/echo description = %q", gotDesc["echo"])
	}
}

func TestTelegramChannel_TelegramRootOverride(t *testing.T) {
	b := bus.NewMessageBus(10)
	ch, err := NewTelegramChannel(config.TelegramConfig{Token: fakeToken, RootDir: "/tmp/custom-telegram"}, b)
	if err != nil {
		t.Fatalf("NewTelegramChannel error: %v", err)
	}
	ch.SetWorkspace("/tmp/workspace")
	if got := ch.telegramRoot(); got != "/tmp/custom-telegram" {
		t.Fatalf("telegramRoot = %q, want /tmp/custom-telegram", got)
	}
}

func TestTelegramChannel_SyncBotCommands(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Token: fakeToken})
	ch.slashCommands = map[string]telegram.Command{
		"compact": {Name: "compact", Description: "Compress conversation history"},
		"status":  {Name: "status", Description: "Check bot status"},
	}

	if err := ch.syncBotCommands(context.Background()); err != nil {
		t.Fatalf("syncBotCommands error: %v", err)
	}

	var payloads []struct {
		Commands []struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		} `json:"commands"`
		Scope *struct {
			Type string `json:"type"`
		} `json:"scope,omitempty"`
	}

	for _, call := range caller.calls {
		if !strings.HasSuffix(call.URL, "/setMyCommands") {
			continue
		}
		var payload struct {
			Commands []struct {
				Command     string `json:"command"`
				Description string `json:"description"`
			} `json:"commands"`
			Scope *struct {
				Type string `json:"type"`
			} `json:"scope,omitempty"`
		}
		if err := json.Unmarshal(call.Data.BodyRaw, &payload); err != nil {
			t.Fatalf("unmarshal setMyCommands payload: %v", err)
		}
		payloads = append(payloads, payload)
	}

	if len(payloads) != 2 {
		t.Fatalf("setMyCommands call count = %d, want 2", len(payloads))
	}

	for i, payload := range payloads {
		if len(payload.Commands) != 3 {
			t.Fatalf("payload %d command count = %d, want 3", i, len(payload.Commands))
		}
		names := []string{payload.Commands[0].Command, payload.Commands[1].Command, payload.Commands[2].Command}
		if strings.Join(names, ",") != "compact,new,status" {
			t.Fatalf("payload %d commands = %v, want [compact new status]", i, names)
		}
	}

	if payloads[0].Scope != nil {
		t.Fatalf("first payload scope = %+v, want nil", payloads[0].Scope)
	}
	if payloads[1].Scope == nil || payloads[1].Scope.Type != "all_private_chats" {
		t.Fatalf("second payload scope = %+v, want all_private_chats", payloads[1].Scope)
	}
}

// === Status Card Tests ===

func TestStatusCard_Render_Empty(t *testing.T) {
	card := telegram.NewStatusCard()
	html := card.Render()
	if !strings.Contains(html, "Working...") {
		t.Errorf("expected Working... in card, got %q", html)
	}
	if !strings.Contains(html, "⏱") {
		t.Errorf("expected timer in card, got %q", html)
	}
}

func TestStatusCard_Render_WithTools(t *testing.T) {
	card := telegram.NewStatusCard()
	card.AddTool("t1", "Read", "config.go")
	card.AddTool("t2", "Grep", "handleAuth")
	card.FinishTool("t1", false)
	html := card.Render()
	if !strings.Contains(html, "✅") {
		t.Error("expected ✅ for finished tool")
	}
	if !strings.Contains(html, "⏳") {
		t.Error("expected ⏳ for running tool")
	}
	if !strings.Contains(html, "Read") {
		t.Error("expected tool name Read")
	}
	if !strings.Contains(html, "config.go") {
		t.Error("expected tool summary config.go")
	}
}
func TestStatusCard_Render_WithIteration(t *testing.T) {
	card := telegram.NewStatusCard()
	card.SetIteration(3)
	card.AddTool("t1", "Bash", "ls -la")
	html := card.Render()
	if !strings.Contains(html, "Iteration 3") {
		t.Errorf("expected Iteration 3 in card, got %q", html)
	}
}
func TestStatusCard_FinishTool_Error(t *testing.T) {
	card := telegram.NewStatusCard()
	card.AddTool("t1", "Edit", "main.go")
	card.FinishTool("t1", true)
	html := card.Render()
	if !strings.Contains(html, "❌") {
		t.Error("expected ❌ for failed tool")
	}
}
func TestSummarizeToolInput(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{"file path", "Read", `{"filePath":"/src/main.go"}`, "/src/main.go"},
		{"command", "Bash", `{"command":"ls -la"}`, "ls -la"},
		{"query", "Grep", `{"query":"handleAuth","include":"*.go"}`, "handleAuth"},
		{"empty", "Read", `{}`, ""},
		{"invalid json", "Read", `not json`, ""},
		{"long path", "Read", `{"filePath":"/very/long/path/that/exceeds/forty/characters/limit/file.go"}`, "/very/long/path/that/exceeds/forty/ch..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := telegram.SummarizeToolInput(tt.tool, json.RawMessage(tt.input))
			if got != tt.want {
				t.Errorf("telegram.SummarizeToolInput(%s) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}
func TestTelegramChannel_SendStream_PureText(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Streaming: true, Feedback: "normal"})
	events := make(chan api.StreamEvent, 10)
	// Pure text — no tool events, should skip status card
	events <- api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "hello "}}
	events <- api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "world"}}
	close(events)
	err := ch.SendStream(context.Background(), "123", nil, events)
	if err != nil {
		t.Fatalf("SendStream error: %v", err)
	}
	var sendCount, deleteCount int
	var silentSend, normalSend int
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			sendCount++
			if c.Data != nil && len(c.Data.BodyRaw) > 0 {
				var payload map[string]any
				if err := json.Unmarshal(c.Data.BodyRaw, &payload); err == nil {
					if v, ok := payload["disable_notification"].(bool); ok && v {
						silentSend++
					} else {
						normalSend++
					}
				}
			}
		}
		if strings.HasSuffix(c.URL, "/deleteMessage") {
			deleteCount++
		}
	}
	// Content message is now ticker-driven (not created on first delta),
	// so with synchronous events only the final report is sent.
	if sendCount < 1 {
		t.Errorf("expected at least 1 sendMessage call (final), got %d", sendCount)
	}
	if normalSend == 0 {
		t.Error("expected final report to be sent as a normal notification message")
	}
}
func TestTelegramChannel_SendStream_WithTools(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Streaming: true, Feedback: "debug"})
	events := make(chan api.StreamEvent, 20)
	iter := 0
	// Simulate: iteration_start -> content_block(tool_use) -> tool_execution -> text
	events <- api.StreamEvent{Type: api.EventIterationStart, Iteration: &iter}
	events <- api.StreamEvent{Type: api.EventContentBlockStart, ContentBlock: &api.ContentBlock{Type: "tool_use", ID: "t1", Name: "Read"}}
	events <- api.StreamEvent{Type: api.EventContentBlockStop}
	events <- api.StreamEvent{Type: api.EventToolExecutionStart, ToolUseID: "t1", Name: "Read", Iteration: &iter}
	events <- api.StreamEvent{Type: api.EventToolExecutionResult, ToolUseID: "t1", Name: "Read"}
	events <- api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "result"}}
	close(events)
	err := ch.SendStream(context.Background(), "123", nil, events)
	if err != nil {
		t.Fatalf("SendStream error: %v", err)
	}
	var sendCount, deleteCount int
	var silentSend, normalSend int
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			sendCount++
			if c.Data != nil && len(c.Data.BodyRaw) > 0 {
				var payload map[string]any
				if err := json.Unmarshal(c.Data.BodyRaw, &payload); err == nil {
					if v, ok := payload["disable_notification"].(bool); ok && v {
						silentSend++
					} else {
						normalSend++
					}
				}
			}
		}
		if strings.HasSuffix(c.URL, "/deleteMessage") {
			deleteCount++
		}
	}
	// Status card is event-driven; content message is ticker-driven.
	// With synchronous events, only status card + final report are sent.
	if sendCount < 2 {
		t.Errorf("expected at least 2 sendMessage calls (status + final), got %d", sendCount)
	}
	if deleteCount < 1 {
		t.Errorf("expected at least 1 deleteMessage call for status cleanup, got %d", deleteCount)
	}
	if silentSend < 1 {
		t.Errorf("expected at least 1 silent intermediate message, got %d", silentSend)
	}
	if normalSend == 0 {
		t.Error("expected final report to be sent as a normal notification message")
	}
}
func TestTelegramChannel_SendStream_Disabled(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Streaming: false})
	events := make(chan api.StreamEvent, 5)
	events <- api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "buffered"}}
	close(events)
	err := ch.SendStream(context.Background(), "123", nil, events)
	if err != nil {
		t.Fatalf("SendStream error: %v", err)
	}
	var hasSend bool
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			hasSend = true
		}
	}
	if !hasSend {
		t.Error("expected sendMessage for non-streaming mode")
	}
}

func TestTelegramChannel_SendStream_ErrorVisibility(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Streaming: true, Feedback: "normal"})
	events := make(chan api.StreamEvent, 5)
	events <- api.StreamEvent{Type: api.EventError, Output: "unexpected end of JSON input"}
	close(events)

	if err := ch.SendStream(context.Background(), "123", nil, events); err != nil {
		t.Fatalf("SendStream error: %v", err)
	}

	finalText := ""
	for i := len(caller.calls) - 1; i >= 0; i-- {
		c := caller.calls[i]
		if !strings.HasSuffix(c.URL, "/sendMessage") || c.Data == nil || len(c.Data.BodyRaw) == 0 {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(c.Data.BodyRaw, &payload); err != nil {
			continue
		}
		if text, ok := payload["text"].(string); ok {
			finalText = text
			break
		}
	}

	if !strings.Contains(finalText, "stream failed: unexpected end of JSON input") {
		t.Fatalf("final message = %q, want stream error visible", finalText)
	}
	if strings.Contains(finalText, "agent return null") {
		t.Fatalf("final message should not fallback to null, got %q", finalText)
	}
}

func TestTelegramChannel_SendStream_FinalSendFailureKeepsIntermediateMessages(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Streaming: true, Feedback: "debug"})
	caller.methodErrSeq = map[string][]error{
		"sendMessage": {nil, errors.New("final send failed"), errors.New("final send failed")},
	}

	events := make(chan api.StreamEvent, 10)
	iter := 0
	events <- api.StreamEvent{Type: api.EventIterationStart, Iteration: &iter}
	events <- api.StreamEvent{Type: api.EventToolExecutionStart, ToolUseID: "t1", Name: "Read", Iteration: &iter}
	events <- api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Type: "text_delta", Text: "partial result"}}
	close(events)

	err := ch.SendStream(context.Background(), "123", nil, events)
	if err == nil || !strings.Contains(err.Error(), "final send failed") {
		t.Fatalf("SendStream error = %v, want final send failure", err)
	}

	var sendCount, deleteCount int
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			sendCount++
		}
		if strings.HasSuffix(c.URL, "/deleteMessage") {
			deleteCount++
		}
	}
	if sendCount < 3 {
		t.Fatalf("expected intermediate + final send attempts, got %d", sendCount)
	}
	if deleteCount != 0 {
		t.Fatalf("expected no deleteMessage when final send fails, got %d", deleteCount)
	}
}

func TestTelegramChannel_SaveFile_SanitizesName(t *testing.T) {
	ch, _ := newTestChannel(t, config.TelegramConfig{})
	tmpDir := t.TempDir()
	ch.SetWorkspace(tmpDir)

	payload := []byte("hello")
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer downloadServer.Close()

	serverURL, err := url.Parse(downloadServer.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	ch.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clonedReq := req.Clone(req.Context())
		clonedReq.URL.Scheme = serverURL.Scheme
		clonedReq.URL.Host = serverURL.Host
		return transport.RoundTrip(clonedReq)
	})}

	savedPath, err := ch.saveFile("f1", "../nested/evil.txt")
	if err != nil {
		t.Fatalf("saveFile error: %v", err)
	}
	wantDir := filepath.Join(tmpDir, "uploads")
	if filepath.Dir(savedPath) != wantDir {
		t.Fatalf("saved dir = %q, want %q", filepath.Dir(savedPath), wantDir)
	}
	if strings.Contains(savedPath, "nested") {
		t.Fatalf("saved path should be sanitized, got %q", savedPath)
	}
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("saved content = %q, want %q", string(data), string(payload))
	}
}

func TestTelegramChannel_SendReaction_SkipsMissingMessageID(t *testing.T) {
	ch, caller := newTestChannel(t, config.TelegramConfig{Feedback: "normal"})
	ch.sendReaction(123, 0, "👍")
	for _, c := range caller.calls {
		if strings.HasSuffix(c.URL, "/setMessageReaction") {
			t.Fatal("expected no setMessageReaction call for missing message id")
		}
	}
}
