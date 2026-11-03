package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #65: a user message projects the structured multiview request it was sent
// with, so regenerating the reply sends the same group, model and pictures.
func TestChatMessageWireCarriesTheMultiviewRequest(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	server := &Server{application: application}
	field := application.Chat.API.ResponseFields.Multiview
	if field == "" {
		t.Fatal("the shipped Schema must name the message multiview member")
	}
	request := &chat.MultiviewRequest{Group: "run1", Model: "tripo-v3.1-multiview", Views: []chat.MultiviewView{
		{View: "front", TaskID: "t1", AssetID: "x1"}, {View: "back", AssetID: "up3"}}}
	wire := server.chatMessageWire("", chat.Message{ID: "m1", Role: "user", Content: "go", Multiview: request})
	encoded, err := json.Marshal(wire[field])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"group":"run1","model":"tripo-v3.1-multiview","views":[{"view":"front","task_id":"t1","asset_id":"x1"},{"view":"back","asset_id":"up3"}]}`; string(encoded) != want {
		t.Fatalf("multiview member = %s, want %s", encoded, want)
	}
	if _, present := server.chatMessageWire("", chat.Message{ID: "m2", Role: "user", Content: "plain"})[field]; present {
		t.Fatal("a message without a request must not carry the member")
	}
}
