package auth

import (
	"context"
	"database/sql"
	"strings"
)

// OperatorHouseholdMember is one person in a household for the operator admin list.
type OperatorHouseholdMember struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

// OperatorHouseholdSummary is one household row on the operator admin list.
type OperatorHouseholdSummary struct {
	ID             string                    `json:"id"`
	Name           string                    `json:"name"`
	IsPrimary      bool                      `json:"isPrimary"`
	RestockMode    string                    `json:"restockMode"`
	CreatedAt      string                    `json:"createdAt"`
	DeletedAt      string                    `json:"deletedAt,omitempty"`
	PurgeAfter     string                    `json:"purgeAfter,omitempty"`
	Members        []OperatorHouseholdMember `json:"members"`
	MemberCount    int                       `json:"memberCount"`
	ProductCount   int                       `json:"productCount"`
	ItemsOnHand    int                       `json:"itemsOnHand"`
	PendingScans   int                       `json:"pendingScans"`
	LastActivityAt string                    `json:"lastActivityAt,omitempty"`
}

// ListAllHouseholdsForOperator returns every household in the product for the operator account.
func (s *Service) ListAllHouseholdsForOperator(ctx context.Context) ([]OperatorHouseholdSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			h.id,
			h.name,
			h.is_primary,
			COALESCE(NULLIF(h.restock_mode, ''), 'auto'),
			h.created_at,
			COALESCE(h.deleted_at, ''),
			COALESCE(h.purge_after, ''),
			(SELECT COUNT(*) FROM memberships m WHERE m.household_id = h.id),
			(SELECT COUNT(DISTINCT i.product_id) FROM items i WHERE i.household_id = h.id),
			(SELECT COUNT(*) FROM item_instances inst
			 WHERE inst.household_id = h.id AND inst.removed_at IS NULL),
			(SELECT COUNT(*) FROM scan_entries se
			 WHERE se.household_id = h.id AND se.status = 'pending'),
			COALESCE((SELECT MAX(ts) FROM (
				SELECT sm.at AS ts FROM stock_moves sm WHERE sm.household_id = h.id
				UNION ALL
				SELECT se.scanned_at AS ts FROM scan_entries se WHERE se.household_id = h.id
			)), '')
		FROM households h
		ORDER BY h.is_primary DESC, h.created_at ASC, h.id ASC`)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()

	out := []OperatorHouseholdSummary{}
	for rows.Next() {
		var row OperatorHouseholdSummary
		var primary int
		var deleted, purge, lastActivity string
		if err := rows.Scan(
			&row.ID, &row.Name, &primary, &row.RestockMode, &row.CreatedAt,
			&deleted, &purge, &row.MemberCount, &row.ProductCount, &row.ItemsOnHand,
			&row.PendingScans, &lastActivity,
		); err != nil {
			return nil, AsError(err)
		}
		row.IsPrimary = primary != 0
		row.DeletedAt = strings.TrimSpace(deleted)
		row.PurgeAfter = strings.TrimSpace(purge)
		row.LastActivityAt = strings.TrimSpace(lastActivity)
		row.Members = []OperatorHouseholdMember{}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, AsError(err)
	}
	if len(out) == 0 {
		return out, nil
	}
	if err := s.attachOperatorMembers(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) attachOperatorMembers(ctx context.Context, households []OperatorHouseholdSummary) error {
	ids := make([]string, len(households))
	byID := map[string]*OperatorHouseholdSummary{}
	for i := range households {
		ids[i] = households[i].ID
		byID[households[i].ID] = &households[i]
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.household_id, m.user_id, u.email_norm, u.name, m.role, m.created_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.household_id IN (`+placeholders+`) AND u.deleted_at IS NULL
		ORDER BY m.household_id,
			CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,
			m.created_at ASC,
			u.id ASC`, args...)
	if err != nil {
		return AsError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var houseID string
		var member OperatorHouseholdMember
		if err := rows.Scan(&houseID, &member.UserID, &member.Email, &member.Name, &member.Role, &member.CreatedAt); err != nil {
			return AsError(err)
		}
		if h := byID[houseID]; h != nil {
			h.Members = append(h.Members, member)
		}
	}
	return AsError(rows.Err())
}

// OperatorForbidden is returned when a signed-in user who is not the operator calls an operator route.
func OperatorForbidden() *Error {
	return fail(CodeForbiddenOperator, SentenceForbiddenOperator)
}

// IsOperator reports whether userID is the product operator account.
func (s *Service) IsOperator(ctx context.Context, userID string) (bool, error) {
	var op int
	err := s.db.QueryRowContext(ctx, `SELECT operator FROM users WHERE id = ? AND deleted_at IS NULL`, userID).Scan(&op)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, AsError(err)
	}
	return op == 1, nil
}
