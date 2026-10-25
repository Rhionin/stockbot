package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestStreamKickFollowsTheMemberNotTheKey(t *testing.T) {
	var none *Streams
	if ctx, err := none.BindLive(context.Background(), "user:a", "house-north", "user-a", "account"); err != nil || ctx == nil {
		t.Fatalf("nil streams = %v", err)
	}
	none.KickHousehold("house-north", "user-a")
	none.Kick("user:a")
	if streamMatch(nil, "house-north", "user-a") {
		t.Fatal("nil entry matched")
	}

	streams := NewStreams()
	streams.KickHousehold("", "")
	streams.Kick("")
	north, err := streams.BindLive(context.Background(), "user:a", "house-north", "user-a", "account")
	if err != nil {
		t.Fatal(err)
	}
	south, err := streams.BindLive(context.Background(), "user:a", "house-south", "user-a", "account")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := streams.BindLive(context.Background(), "user:b", "house-north", "user-b", "account")
	if err != nil {
		t.Fatal(err)
	}
	anonymous, err := streams.BindLive(context.Background(), "anon:north", "house-north", "", "account")
	if err != nil {
		t.Fatal(err)
	}

	streams.KickHousehold("house-north", "user-a")
	waitDone(t, north)
	if south.Err() != nil {
		t.Fatal("removing a member closed their other household")
	}
	streams.KickHousehold("", "user-a")
	waitDone(t, south)
	if kept.Err() != nil || anonymous.Err() != nil {
		t.Fatal("a user kick closed someone else")
	}
	streams.KickHousehold("house-north", "")
	waitDone(t, anonymous)
	if kept.Err() != nil {
		t.Fatal("a household-wide kick closed an account stream with a user id")
	}
}

func TestServiceDBIsNilSafe(t *testing.T) {
	var svc *Service
	if svc.DB() != nil {
		t.Fatal("nil service returned a database")
	}
	db := &sql.DB{}
	svc = NewService(db, t.TempDir())
	if svc.DB() != db {
		t.Fatal("DB did not return the pantry connection")
	}
	svc.SetLive(nil)
	svc.kickLive("house-north", "user-a")
}

func waitDone(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stream stayed open")
	}
}
