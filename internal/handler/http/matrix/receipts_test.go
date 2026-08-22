package matrix_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) sendReceipt(t *testing.T, host string, session sessionBody, roomID, receiptType,
	eventID string, body map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPost, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/receipt/"+receiptType+"/"+url.PathEscape(eventID),
		session.AccessToken, body)
}

func (s *server) mustSendReceipt(t *testing.T, host string, session sessionBody, roomID, receiptType,
	eventID string, body map[string]any,
) {
	t.Helper()
	if rec := s.sendReceipt(t, host, session, roomID, receiptType, eventID, body); rec.Code != http.StatusOK {
		t.Fatalf("receipt %s on %s = %d: %s", receiptType, eventID, rec.Code, rec.Body)
	}
}

func (s *server) message(t *testing.T, host string, session sessionBody, roomID, txn string,
	content map[string]any,
) string {
	t.Helper()
	rec := s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/send/m.room.message/"+txn,
		session.AccessToken, content)
	if rec.Code != http.StatusOK {
		t.Fatalf("send = %d: %s", rec.Code, rec.Body)
	}
	return decode[struct {
		EventID string `json:"event_id"`
	}](t, rec).EventID
}

func TestReadStateIsExactPerThreadAndPerRoom(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)

	room := s.seedRoom(t, of, alice)
	joined := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/join", bob.AccessToken, map[string]any{})
	if joined.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", joined.Code, joined.Body)
	}

	rootA := s.message(t, of.ServerName, alice, room.RoomID, "a", text("thread root A"))
	rootB := s.message(t, of.ServerName, alice, room.RoomID, "b", text("thread root B"))
	inA := s.message(t, of.ServerName, alice, room.RoomID, "a1", threaded(rootA, "in A"))
	inB := s.message(t, of.ServerName, alice, room.RoomID, "b1", threaded(rootB, "in B"))
	main := s.message(t, of.ServerName, alice, room.RoomID, "m", text("main timeline"))

	upTo := func(threadID string) int64 {
		t.Helper()
		position, err := s.receipts.ReadUpTo(t.Context(), of.Scope(), bob.UserID, room.RoomID, threadID)
		if err != nil {
			t.Fatalf("ReadUpTo(%q): %v", threadID, err)
		}
		return position
	}
	unread := func(threadID string) int {
		t.Helper()
		count, err := s.receipts.Unread(t.Context(), of.Scope(), bob.UserID, room.RoomID, threadID)
		if err != nil {
			t.Fatalf("Unread(%q): %v", threadID, err)
		}
		return count
	}

	if upTo(entity.ThreadUnthreaded) != 0 || upTo(entity.ThreadMain) != 0 {
		t.Fatal("a room with no receipts reports a read-up-to point")
	}
	before := unread(entity.ThreadUnthreaded)
	if before == 0 {
		t.Fatal("nothing is unread before any receipt, so the count proves nothing")
	}

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, rootA,
		map[string]any{"thread_id": entity.ThreadMain})
	if upTo(entity.ThreadMain) == 0 {
		t.Fatal("a main-timeline receipt did not move the main read-up-to point")
	}
	if upTo(entity.ThreadUnthreaded) != 0 {
		t.Fatal("a main-timeline receipt moved the unthreaded read-up-to point")
	}

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, inA,
		map[string]any{"thread_id": rootA})
	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, inB,
		map[string]any{"thread_id": rootB})

	inThreadA, inThreadB := upTo(rootA), upTo(rootB)
	if inThreadA == 0 || inThreadB == 0 || inThreadA == inThreadB {
		t.Fatalf("threads share a read-up-to point: A=%d B=%d", inThreadA, inThreadB)
	}
	if upTo(entity.ThreadMain) >= inThreadA {
		t.Fatal("a threaded receipt moved the main timeline")
	}

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, main, map[string]any{})
	unthreaded := upTo(entity.ThreadUnthreaded)
	if unthreaded == 0 {
		t.Fatal("an unthreaded receipt did not move the unthreaded read-up-to point")
	}
	if unread(entity.ThreadUnthreaded) != 0 {
		t.Fatalf("%d events unread after reading to the end", unread(entity.ThreadUnthreaded))
	}
	if upTo(rootA) != inThreadA {
		t.Fatal("an unthreaded receipt moved a thread's read-up-to point")
	}
}

func TestAPrivateReceiptBehindThePublicOneDoesNotRewind(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)

	room := s.seedRoom(t, of, alice)
	if rec := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/join", bob.AccessToken,
		map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body)
	}

	early := s.message(t, of.ServerName, alice, room.RoomID, "one", text("first"))
	s.message(t, of.ServerName, alice, room.RoomID, "two", text("second"))
	late := s.message(t, of.ServerName, alice, room.RoomID, "three", text("third"))

	upTo := func() int64 {
		t.Helper()
		position, err := s.receipts.ReadUpTo(t.Context(), of.Scope(), bob.UserID, room.RoomID,
			entity.ThreadUnthreaded)
		if err != nil {
			t.Fatalf("ReadUpTo: %v", err)
		}
		return position
	}

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, late, map[string]any{})
	ahead := upTo()

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptReadPrivate, early, map[string]any{})
	if got := upTo(); got != ahead {
		t.Fatalf("a private receipt behind the public one rewound the read-up-to point from %d to %d", ahead, got)
	}

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptReadPrivate, late, map[string]any{})
	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, early, map[string]any{})
	if got := upTo(); got != ahead {
		t.Fatalf("a public receipt moved back rewound the read-up-to point to %d while the private one was at %d",
			got, ahead)
	}

	count, err := s.receipts.Unread(t.Context(), of.Scope(), bob.UserID, room.RoomID, entity.ThreadUnthreaded)
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if count < 0 {
		t.Fatalf("the unread count went negative: %d", count)
	}
}

func TestAPrivateReceiptIsNeverShownToAnotherUser(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)

	room := s.seedRoom(t, of, alice)
	if rec := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/join", bob.AccessToken,
		map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body)
	}
	eventID := s.message(t, of.ServerName, alice, room.RoomID, "one", text("hello"))

	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptReadPrivate, eventID, map[string]any{})
	s.mustSendReceipt(t, of.ServerName, bob, room.RoomID, entity.ReceiptRead, eventID, map[string]any{})

	seenByAlice, err := s.receipts.ForRoom(t.Context(), of.Scope(), alice.UserID, room.RoomID)
	if err != nil {
		t.Fatalf("ForRoom: %v", err)
	}
	for _, receipt := range seenByAlice {
		if receipt.Type == entity.ReceiptReadPrivate {
			t.Fatalf("alice can see bob's private receipt: %+v", receipt)
		}
	}

	seenByBob, err := s.receipts.ForRoom(t.Context(), of.Scope(), bob.UserID, room.RoomID)
	if err != nil {
		t.Fatalf("ForRoom: %v", err)
	}
	var own bool
	for _, receipt := range seenByBob {
		if receipt.Type == entity.ReceiptReadPrivate && receipt.UserID == bob.UserID {
			own = true
		}
	}
	if !own {
		t.Fatal("bob cannot see his own private receipt")
	}
}

func TestReadMarkersWriteTheFullyReadRoomAccountData(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	room := s.seedRoom(t, of, alice)
	eventID := s.message(t, of.ServerName, alice, room.RoomID, "one", text("hello"))

	marked := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/read_markers", alice.AccessToken,
		map[string]any{"m.fully_read": eventID, "m.read": eventID})
	if marked.Code != http.StatusOK {
		t.Fatalf("read markers = %d: %s", marked.Code, marked.Body)
	}

	stored := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, room.RoomID,
		entity.AccountDataFullyRead))
	if stored["event_id"] != eventID {
		t.Fatalf("m.fully_read = %v, want %s", stored["event_id"], eventID)
	}

	found, err := s.receipts.ForRoom(t.Context(), of.Scope(), alice.UserID, room.RoomID)
	if err != nil {
		t.Fatalf("ForRoom: %v", err)
	}
	var read bool
	for _, receipt := range found {
		if receipt.Type == entity.ReceiptRead && receipt.EventID == eventID {
			read = true
		}
	}
	if !read {
		t.Fatalf("read_markers did not also create the m.read receipt: %+v", found)
	}

	refused := s.putAccountData(t, of.ServerName, alice, room.RoomID, entity.AccountDataFullyRead,
		map[string]any{"event_id": eventID})
	if refused.Code != http.StatusMethodNotAllowed {
		t.Fatalf("a client wrote m.fully_read directly = %d, want 405", refused.Code)
	}
}

func TestAReceiptForAnEventOutsideTheRoomIsRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	stranger := s.register(t, of.ServerName, "eve", goodPassword)

	first := s.seedRoom(t, of, alice)
	second := s.createRoom(t, of.ServerName, alice.AccessToken, map[string]any{})
	elsewhere := s.message(t, of.ServerName, alice, second, "one", text("hello"))

	if rec := s.sendReceipt(t, of.ServerName, alice, first.RoomID, entity.ReceiptRead, elsewhere,
		map[string]any{}); rec.Code != http.StatusNotFound {
		t.Fatalf("a receipt for another room's event = %d, want 404: %s", rec.Code, rec.Body)
	}

	inRoom := s.message(t, of.ServerName, alice, first.RoomID, "two", text("hello"))
	if rec := s.sendReceipt(t, of.ServerName, stranger, first.RoomID, entity.ReceiptRead, inRoom,
		map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("a receipt from a non-member = %d, want 403: %s", rec.Code, rec.Body)
	}

	if rec := s.sendReceipt(t, of.ServerName, alice, first.RoomID, "m.read.invented", inRoom,
		map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown receipt type = %d, want 400: %s", rec.Code, rec.Body)
	}
	if rec := s.sendReceipt(t, of.ServerName, alice, first.RoomID, entity.ReceiptRead, inRoom,
		map[string]any{"thread_id": inRoom}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a thread_id naming a non-root = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestReceiptsOfOneDomainAreInvisibleToAnother(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	beta := s.open(t, "beta.test")

	here := s.register(t, alpha.ServerName, "alice", goodPassword)
	there := s.register(t, beta.ServerName, "alice", goodPassword)
	hereRoom := s.seedRoom(t, alpha, here)
	thereRoom := s.seedRoom(t, beta, there)

	hereFound, err := s.receipts.ForRoom(t.Context(), alpha.Scope(), here.UserID, hereRoom.RoomID)
	if err != nil {
		t.Fatalf("ForRoom: %v", err)
	}
	if len(hereFound) == 0 {
		t.Fatal("the seeded receipt is missing, so the isolation check proves nothing")
	}

	if _, err := s.receipts.ForRoom(t.Context(), alpha.Scope(), here.UserID, thereRoom.RoomID); err == nil {
		t.Fatal("a caller of alpha.test read receipts from a beta.test room")
	}

	var count int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM receipts WHERE tenant_id = $1`, beta.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count == 0 {
		t.Fatal("beta.test has no receipts, so the tenant split proves nothing")
	}
}
