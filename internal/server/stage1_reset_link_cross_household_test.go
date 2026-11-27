package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

// TestStage1ResetLinkForeignHouseholdMemberReturnsNotFound (#646) pins that an
// Alder owner cannot mint a password-reset link for a user who belongs only to
// Birch, and that no reset_links row is written.
func TestStage1ResetLinkForeignHouseholdMemberReturnsNotFound(t *testing.T) {
	db, _, handler, alderOwner, alderHouse, _ := stage1Owner(t, "Alder Owner Q646", "alder-owner-q646@pantry.test")
	const alderName = "Household Alder"
	const birchName = "Household Birch"

	if rec := alderOwner.authed(http.MethodPatch, "/api/households/"+alderHouse, map[string]string{"name": alderName}); rec.Code != http.StatusOK {
		t.Fatalf("rename alder %d %s", rec.Code, rec.Body.String())
	}

	start := alderOwner.authed(http.MethodPost, "/api/operator/household-invites", nil)
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil || start.Code != http.StatusCreated {
		t.Fatalf("operator invite %d %s", start.Code, start.Body.String())
	}
	created := alderOwner.authed(http.MethodPost, "/api/households", map[string]string{
		"name": birchName, "inviteCode": startInvite.Code,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create birch %d %s", created.Code, created.Body.String())
	}
	var birch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &birch); err != nil || birch.ID == "" {
		t.Fatal(created.Body.String())
	}

	switchHouse := alderOwner.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": birch.ID})
	if switchHouse.Code != http.StatusOK {
		t.Fatalf("switch birch %d %s", switchHouse.Code, switchHouse.Body.String())
	}
	birchOwner := &stage1Caller{t: t, h: handler, cookie: alderOwner.cookie, house: birch.ID}

	join := birchOwner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var joinInvite auth.InviteCreated
	if err := json.Unmarshal(join.Body.Bytes(), &joinInvite); err != nil || join.Code != http.StatusCreated {
		t.Fatalf("birch invite %d %s", join.Code, join.Body.String())
	}
	birchMember := &stage1Caller{t: t, h: handler}
	accept := birchMember.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": joinInvite.Code,
		"account": map[string]string{
			"name": "Birch Member Q646", "email": "birch-member-q646@pantry.test", "password": "correct horse",
		},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatalf("accept %d %s", accept.Code, accept.Body.String())
	}
	var birchSession auth.SessionView
	if err := json.Unmarshal(accept.Body.Bytes(), &birchSession); err != nil || birchSession.User == nil {
		t.Fatal(accept.Body.String())
	}
	birchUserID := birchSession.User.ID

	back := alderOwner.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": alderHouse})
	if back.Code != http.StatusOK {
		t.Fatalf("switch alder %d %s", back.Code, back.Body.String())
	}
	alderOwner.house = alderHouse

	var linksBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reset_links WHERE user_id = ?`, birchUserID).Scan(&linksBefore); err != nil {
		t.Fatal(err)
	}

	reset := alderOwner.authed(http.MethodPost, "/api/households/"+alderHouse+"/members/"+birchUserID+"/reset-link", nil)
	if reset.Code != http.StatusNotFound || !strings.Contains(reset.Body.String(), `"code":"not_found"`) {
		t.Fatalf("cross-household reset %d %s", reset.Code, reset.Body.String())
	}
	if strings.Contains(reset.Body.String(), `"code":"forbidden_role"`) || reset.Code == http.StatusForbidden || reset.Code == http.StatusUnauthorized {
		t.Fatalf("cross-household reset must be not_found: %d %s", reset.Code, reset.Body.String())
	}
	if strings.Contains(reset.Body.String(), "sbr_") {
		t.Fatalf("response leaked reset code: %s", reset.Body.String())
	}

	var linksAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reset_links WHERE user_id = ?`, birchUserID).Scan(&linksAfter); err != nil {
		t.Fatal(err)
	}
	if linksAfter != linksBefore {
		t.Fatalf("reset_links for birch user %d -> %d", linksBefore, linksAfter)
	}

	login := birchMember.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "birch-member-q646@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("birch member login after rejected reset %d %s", login.Code, login.Body.String())
	}
}