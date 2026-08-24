package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestChatStoreRoundtrip verifies save, reload-after-reopen (persistence) and
// delete against a temp SQLite database.
func TestChatStoreRoundtrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chats.db")

	store, err := OpenChatStore(dbPath)
	if err != nil {
		t.Fatalf("OpenChatStore: %v", err)
	}

	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "**hi** there"},
	}
	meta := &ChatMeta{Title: "Test chat"}
	if err := store.SaveChat(meta, msgs); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	id := meta.ID
	if id == "" {
		t.Fatal("SaveChat did not assign an id")
	}

	// Reopen to prove the data is on disk, not memory.
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	store, err = OpenChatStore(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store.Close()

	got, gotMsgs, err := store.GetChat(id)
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if got.Title != "Test chat" {
		t.Errorf("title = %q, want %q", got.Title, "Test chat")
	}
	if !reflect.DeepEqual(gotMsgs, msgs) {
		t.Errorf("messages = %+v, want %+v", gotMsgs, msgs)
	}

	// Upsert replaces the message list.
	newMsgs := []ChatMessage{{Role: "user", Content: "updated"}}
	if err := store.SaveChat(&ChatMeta{ID: id, Title: "Renamed"}, newMsgs); err != nil {
		t.Fatalf("SaveChat upsert: %v", err)
	}
	_, gotMsgs, err = store.GetChat(id)
	if err != nil {
		t.Fatalf("GetChat after upsert: %v", err)
	}
	if !reflect.DeepEqual(gotMsgs, newMsgs) {
		t.Errorf("after upsert messages = %+v, want %+v", gotMsgs, newMsgs)
	}

	// List ordering: newest first.
	list, err := store.ListChats()
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Errorf("list = %+v, want single chat %q", list, id)
	}

	if err := store.DeleteChat(id); err != nil {
		t.Fatalf("DeleteChat: %v", err)
	}
	if _, _, err := store.GetChat(id); err != os.ErrNotExist {
		t.Errorf("GetChat after delete = %v, want os.ErrNotExist", err)
	}
	if err := store.DeleteChat(id); err != os.ErrNotExist {
		t.Errorf("second DeleteChat = %v, want os.ErrNotExist", err)
	}
}
