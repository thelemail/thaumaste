package matrix_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strconv"
	"testing"
)

type soakActor struct {
	session sessionBody
	batch   string
	rooms   map[string]bool
	seen    map[string]int
	told    map[string]bool
}

type soakMessage struct {
	target string
	tag    string
}

func TestDeviceChurnLosesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("the churn soak is long")
	}

	const (
		users  = 8
		rounds = 400
		seed   = 20260822
	)

	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	dice := rand.New(rand.NewPCG(seed, seed))

	actors := make([]*soakActor, 0, users)
	rooms := make([]string, 0, 3)
	for i := range users {
		session := s.register(t, tenant.ServerName, fmt.Sprintf("soak%d", i), goodPassword)
		s.uploadIdentity(t, tenant.ServerName, session, uint64(100+i))
		actors = append(actors, &soakActor{
			session: session,
			rooms:   map[string]bool{},
			seen:    map[string]int{},
			told:    map[string]bool{},
		})
	}
	for i := range 3 {
		rooms = append(rooms, s.createRoom(t, tenant.ServerName,
			actors[i].session.AccessToken, publicRoom))
		actors[i].rooms[rooms[i]] = true
	}
	for _, actor := range actors {
		actor.batch = s.legacySync(t, tenant.ServerName, actor.session.AccessToken, nil).NextBatch
	}

	sent := map[string]soakMessage{}
	expected := map[string]map[string]bool{}

	drain := func(actor *soakActor) {
		body := s.legacySync(t, tenant.ServerName, actor.session.AccessToken,
			url.Values{"since": {actor.batch}})
		actor.batch = body.NextBatch
		for _, raw := range body.ToDevice.Events {
			var carried struct {
				Content struct {
					Tag string `json:"tag"`
				} `json:"content"`
			}
			if err := json.Unmarshal(raw, &carried); err != nil {
				t.Fatalf("decode a to-device message: %v", err)
			}
			actor.seen[carried.Content.Tag]++
		}
		for _, userID := range deviceLists(t, body).Changed {
			actor.told[userID] = true
		}
		for _, userID := range deviceLists(t, body).Left {
			actor.told[userID] = true
		}
	}

	for round := range rounds {
		actor := actors[dice.IntN(len(actors))]
		roomID := rooms[dice.IntN(len(rooms))]

		switch dice.IntN(5) {
		case 0:
			if !actor.rooms[roomID] {
				s.joinRoom(t, tenant.ServerName, roomID, actor.session.AccessToken)
				actor.rooms[roomID] = true
			}
		case 1:
			if actor.rooms[roomID] {
				s.leaveRoom(t, tenant.ServerName, roomID, actor.session.AccessToken)
				delete(actor.rooms, roomID)
			}
		case 2:
			s.uploadIdentity(t, tenant.ServerName, actor.session, uint64(1000+round))
			for _, peer := range actors {
				if peer.session.UserID == actor.session.UserID {
					continue
				}
				if sharesARoom(actor, peer) {
					if expected[peer.session.UserID] == nil {
						expected[peer.session.UserID] = map[string]bool{}
					}
					expected[peer.session.UserID][actor.session.UserID] = true
				}
			}
		case 3:
			target := actors[dice.IntN(len(actors))]
			tag := "tag-" + strconv.Itoa(round)
			s.mustSendToDevice(t, tenant.ServerName, actor.session, "m.room_key", "soak-"+strconv.Itoa(round),
				map[string]any{
					target.session.UserID: map[string]any{
						target.session.DeviceID: map[string]any{"tag": tag},
					},
				})
			sent[tag] = soakMessage{target: target.session.UserID, tag: tag}
		case 4:
			drain(actor)
		}
	}

	for _, actor := range actors {
		drain(actor)
		drain(actor)
	}

	for _, actor := range actors {
		actor.told = map[string]bool{}
	}
	quiet := map[string]map[string]bool{}
	for i, actor := range actors {
		s.uploadIdentity(t, tenant.ServerName, actor.session, uint64(9000+i))
		for _, peer := range actors {
			if peer.session.UserID != actor.session.UserID && sharesARoom(actor, peer) {
				if quiet[peer.session.UserID] == nil {
					quiet[peer.session.UserID] = map[string]bool{}
				}
				quiet[peer.session.UserID][actor.session.UserID] = true
			}
		}
	}
	for _, actor := range actors {
		drain(actor)
	}
	for _, actor := range actors {
		for peer := range quiet[actor.session.UserID] {
			if !actor.told[peer] {
				t.Fatalf("after the churn settled, %s was never told about %s's new device",
					actor.session.UserID, peer)
			}
		}
	}

	for tag, message := range sent {
		for _, actor := range actors {
			got := actor.seen[tag]
			switch {
			case actor.session.UserID == message.target && got != 1:
				t.Fatalf("%s received %s %d times, want exactly once", actor.session.UserID, tag, got)
			case actor.session.UserID != message.target && got != 0:
				t.Fatalf("%s received %s meant for %s", actor.session.UserID, tag, message.target)
			}
		}
	}

	for _, actor := range actors {
		for peer := range expected[actor.session.UserID] {
			if !actor.told[peer] {
				t.Fatalf("%s was never told about %s's device change", actor.session.UserID, peer)
			}
		}
	}
}

func sharesARoom(left, right *soakActor) bool {
	for roomID := range left.rooms {
		if right.rooms[roomID] {
			return true
		}
	}
	return false
}
