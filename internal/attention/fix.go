package attention

// FixTarget holds navigation ids for a needs-attention fix.
type FixTarget struct {
	EntryID    *string `json:"entryId"`
	ItemID     *string `json:"itemId"`
	GroupID    *string `json:"groupId"`
	ProductID  *string `json:"productId"`
	ProviderID *string `json:"providerId"`
}

// Fix is the full wire fix object (rev2 §3.2).
type Fix struct {
	Code    string    `json:"code"`
	Allowed bool      `json:"allowed"`
	Who     *FixWho   `json:"who"`
	Target  FixTarget `json:"target"`
}

type FixWho struct {
	Role string `json:"role"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// fixForFact builds fix for one emittable reason (#686 §3.2, #689 connect_store).
func fixForFact(reason Reason, entryID, itemID, groupID, providerID string, connectAllowed bool) Fix {
	target := FixTarget{
		EntryID:    strPtr(entryID),
		ItemID:     strPtr(itemID),
		GroupID:    strPtr(groupID),
		ProviderID: strPtr(providerID),
	}
	switch reason {
	case ReasonNotConnected, ReasonReconnectRequired:
		fix := Fix{Code: "connect_store", Allowed: connectAllowed, Target: target}
		if !connectAllowed {
			fix.Who = &FixWho{Role: "owner"}
		}
		return fix
	case ReasonSendUnsure:
		return Fix{Code: "resolve_unknown", Allowed: true, Who: nil, Target: target}
	case ReasonSendFailed:
		return Fix{Code: "none", Allowed: false, Who: nil, Target: target}
	case ReasonNotPlanned:
		return Fix{Code: "none", Allowed: false, Who: nil, Target: FixTarget{GroupID: strPtr(groupID)}}
	case ReasonNoBarcode, ReasonNoStoreMatch:
		return Fix{Code: "none", Allowed: false, Who: nil, Target: target}
	default:
		return Fix{Code: "none", Allowed: false, Who: nil, Target: target}
	}
}
