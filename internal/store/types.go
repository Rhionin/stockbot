package store

// Store is one household store in list order.
type Store struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Kind           string      `json:"kind"`
	Position       int         `json:"position"`
	LinkedProvider *string     `json:"linkedProvider"`
	LocationID     *string     `json:"locationId"`
	LocationChain  *string     `json:"locationChain"`
	CartURL        *string     `json:"cartUrl"`
	Check          *StoreCheck `json:"check"`
	CheckReason    *string     `json:"checkReason,omitempty"`
}

// StoreCheck is the checking job state for a linked store (DEP-3).
type StoreCheck struct {
	State      string  `json:"state"`
	StartedAt  string  `json:"startedAt"`
	FinishedAt *string `json:"finishedAt"`
	Checked    int     `json:"checked"`
	Found      int     `json:"found"`
	Total      int     `json:"total"`
	Failure    *string `json:"failure"`
}

// StoresResponse is returned by list, order, and default routes.
type StoresResponse struct {
	Stores         []Store `json:"stores"`
	DefaultStoreID *string `json:"defaultStoreId"`
	LookupsEnabled bool    `json:"lookupsEnabled"`
	CanConnect     bool    `json:"canConnect"`
}

// RemovalPreview counts what changes when a store is removed (DEP-4).
type RemovalPreview struct {
	StoreID       string          `json:"storeId"`
	OpenLines     int             `json:"openLines"`
	OpenLinesNext []OpenLinesNext `json:"openLinesNext"`
	RunOverrides  int             `json:"runOverrides"`
	UsualChoices  int             `json:"usualChoices"`
	PersonChoices int             `json:"personChoices"`
	CheckedItems  int             `json:"checkedItems"`
	TripsKept     int             `json:"tripsKept"`
	IsDefault     bool            `json:"isDefault"`
	IsLinked      bool            `json:"isLinked"`
}

// OpenLinesNext groups open lines by the store they would use after removal.
type OpenLinesNext struct {
	StoreID *string `json:"storeId"`
	Count   int     `json:"count"`
}

// CreateInput is the writable subset of a store.
type CreateInput struct {
	Name           string
	Kind           string
	LinkedProvider *string
	LocationID     *string
	LocationChain  *string
}

// PatchInput holds optional store fields.
type PatchInput struct {
	Name           *string
	Kind           *string
	SetLinked      bool
	LinkedProvider *string
	SetLocation    bool
	LocationID     *string
	LocationChain  *string
}

// CheckingContext carries deployment and cart state for DEP-7 fields.
type CheckingContext struct {
	KrogerDisabled        bool
	CredentialsConfigured bool
}
