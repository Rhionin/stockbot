package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
)

func assertLastOwnerHTTP(t *testing.T, rec *httptest.ResponseRecorder, wantSentence string) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var errBody struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("body %s: %v", rec.Body.String(), err)
	}
	if errBody.Code != auth.CodeLastOwner {
		t.Fatalf("code %q want %q", errBody.Code, auth.CodeLastOwner)
	}
	if errBody.Error != wantSentence {
		t.Fatalf("error %q want %q", errBody.Error, wantSentence)
	}
}

func TestLastOwnerDemoteHouseholdAdminHTTP(t *testing.T) {
	_, _, _, owner, house, userID := stage1Owner(t, "Demote Copy", "demote@pantry.test")
	const want = "You're the only household admin. Make another member a household admin first."
	rec := owner.authed(http.MethodPatch, "/api/households/"+house+"/members/"+userID, map[string]string{"role": "member"})
	assertLastOwnerHTTP(t, rec, want)
}

func TestLastOwnerLeaveHouseholdAdminHTTP(t *testing.T) {
	_, _, _, owner, house, userID := stage1Owner(t, "Leave Copy", "leave@pantry.test")
	const want = "You're the only household admin, so you can't leave yet. Make another member a household admin first, or delete the household."
	rec := owner.authed(http.MethodDelete, "/api/households/"+house+"/members/"+userID, nil)
	assertLastOwnerHTTP(t, rec, want)
	membersRec := owner.authed(http.MethodGet, "/api/households/"+house+"/members", nil)
	if membersRec.Code != http.StatusOK {
		t.Fatalf("members status %d body %s", membersRec.Code, membersRec.Body.String())
	}
	var payload struct {
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	if err := json.Unmarshal(membersRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("members body %s: %v", membersRec.Body.String(), err)
	}
	var found bool
	for _, m := range payload.Members {
		if m.UserID != userID {
			continue
		}
		found = true
		if m.Role != "owner" {
			t.Fatalf("user %q role %q want owner", userID, m.Role)
		}
	}
	if !found {
		t.Fatalf("user %q not in members %#v", userID, payload.Members)
	}
}

func TestLastOwnerAccountDeleteHouseholdAdminHTTP(t *testing.T) {
	_, _, _, owner, _, _ := stage1Owner(t, "Delete Copy", "delete@pantry.test")
	const want = "You're the only household admin of Primary. Make another member a household admin or delete the household first."
	rec := owner.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "delete@pantry.test"})
	assertLastOwnerHTTP(t, rec, want)
}
