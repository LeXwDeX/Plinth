package fakeupstream

// Chat request recording (#247).
//
// The fake OpenAI-compatible chat endpoint records, for tests and the fake
// stack only, what each request carried: the model, each message's role and
// text, and each picture part's detail, whether it is an inline data URI and
// how many bytes it holds. It deliberately keeps no picture bytes and no
// request text beyond a short summary, so a control read cannot leak a whole
// conversation.
//
// This record is a test fixture, not an upstream contract.

// ChatRequestsPath is the control route that returns the recorded chat
// requests and clears them (DELETE).
const ChatRequestsPath = "/fake/control/chat-requests"

// chatRequestRecords bounds how many requests are kept; older ones fall off.
const chatRequestRecords = 50

type ChatRequestRecord struct {
	Model    string              `json:"model"`
	Messages []ChatMessageRecord `json:"messages"`
}

type ChatMessageRecord struct {
	Role   string            `json:"role"`
	Text   string            `json:"text"`
	Images []ChatImageRecord `json:"images"`
}

type ChatImageRecord struct {
	Detail  string `json:"detail"`
	DataURI bool   `json:"data_uri"`
	Bytes   int    `json:"bytes"`
	// Label is the text part that follows the picture, e.g. a handle label.
	Label string `json:"label,omitempty"`
}

// RecordChatRequest appends one recorded request, dropping the oldest record
// when the bound is reached.
func (e *Env) RecordChatRequest(record ChatRequestRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chatRequests = append(e.chatRequests, record)
	if len(e.chatRequests) > chatRequestRecords {
		e.chatRequests = append([]ChatRequestRecord(nil), e.chatRequests[len(e.chatRequests)-chatRequestRecords:]...)
	}
}

// ChatRequests returns the recorded requests, oldest first.
func (e *Env) ChatRequests() []ChatRequestRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ChatRequestRecord(nil), e.chatRequests...)
}

// ResetChatRequests forgets every recorded request.
func (e *Env) ResetChatRequests() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chatRequests = nil
}
