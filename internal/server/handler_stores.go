package server

import (
	"encoding/json"
	"errors"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/store"
)

// StoresHandler serves household store CRUD and ordering.
type StoresHandler struct {
	Stores   *store.Catalog
	Registry *cart.Registry
	Env      ProviderEnv
}

type storePathParams struct {
	ID string `json:"id"`
}

type createStoreBody struct {
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	LinkedProvider *string `json:"linkedProvider"`
	LocationID     *string `json:"locationId"`
	LocationChain  *string `json:"locationChain"`
}

type patchStoreBody struct {
	Name           *string         `json:"name"`
	Kind           *string         `json:"kind"`
	LinkedProvider json.RawMessage `json:"linkedProvider"`
	LocationID     *string         `json:"locationId"`
	LocationChain  *string         `json:"locationChain"`
}

type orderStoresBody struct {
	StoreIDs []string `json:"storeIds"`
}

type defaultStoreBody struct {
	StoreID *string `json:"storeId"`
}

func (h *StoresHandler) checkingCtx() store.CheckingContext {
	chk := store.CheckingContext{KrogerDisabled: h.Env.Disabled}
	if h.Registry != nil {
		chk.CredentialsConfigured = h.Registry.CredentialsConfigured(cart.ProviderID("kroger"))
	}
	return chk
}

func (h *StoresHandler) HandleList(req Request[struct{}, struct{}]) (store.StoresResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return store.StoresResponse{}, InternalError(err)
	}
	out, err := h.Stores.List(req.Context, scope, h.checkingCtx())
	if err != nil {
		return store.StoresResponse{}, storeErr(err)
	}
	return out, nil
}

func (h *StoresHandler) HandleCreate(req Request[createStoreBody, struct{}]) (Created, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, InternalError(err)
	}
	st, err := h.Stores.Create(req.Context, scope, store.CreateInput{
		Name:           req.Body.Name,
		Kind:           req.Body.Kind,
		LinkedProvider: req.Body.LinkedProvider,
		LocationID:     req.Body.LocationID,
		LocationChain:  req.Body.LocationChain,
	})
	if err != nil {
		return Created{}, storeErr(err)
	}
	return Created{Value: st}, nil
}

func (h *StoresHandler) HandlePatch(req Request[patchStoreBody, storePathParams]) (store.Store, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return store.Store{}, InternalError(err)
	}
	patch, err := parsePatchBody(req.Body)
	if err != nil {
		return store.Store{}, err
	}
	st, err := h.Stores.Patch(req.Context, scope, req.PathParams.ID, patch)
	if err != nil {
		return store.Store{}, storeErr(err)
	}
	return st, nil
}

func (h *StoresHandler) HandleRemovalPreview(req Request[struct{}, storePathParams]) (store.RemovalPreview, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return store.RemovalPreview{}, InternalError(err)
	}
	out, err := h.Stores.RemovalPreview(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return store.RemovalPreview{}, storeErr(err)
	}
	return out, nil
}

func (h *StoresHandler) HandleDelete(req Request[struct{}, storePathParams]) (NoContent, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return NoContent{}, InternalError(err)
	}
	if err := h.Stores.Delete(req.Context, scope, req.PathParams.ID); err != nil {
		return NoContent{}, storeErr(err)
	}
	return NoContent{}, nil
}

func (h *StoresHandler) HandleOrder(req Request[orderStoresBody, struct{}]) (store.StoresResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return store.StoresResponse{}, InternalError(err)
	}
	out, err := h.Stores.SetOrder(req.Context, scope, req.Body.StoreIDs, h.checkingCtx())
	if err != nil {
		return store.StoresResponse{}, storeErr(err)
	}
	return out, nil
}

func (h *StoresHandler) HandleDefault(req Request[defaultStoreBody, struct{}]) (store.StoresResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return store.StoresResponse{}, InternalError(err)
	}
	out, err := h.Stores.SetDefault(req.Context, scope, req.Body.StoreID, h.checkingCtx())
	if err != nil {
		return store.StoresResponse{}, storeErr(err)
	}
	return out, nil
}

func parsePatchBody(body patchStoreBody) (store.PatchInput, error) {
	var patch store.PatchInput
	if body.Name != nil {
		patch.Name = body.Name
	}
	if body.Kind != nil {
		patch.Kind = body.Kind
	}
	if len(body.LinkedProvider) > 0 {
		patch.SetLinked = true
		if string(body.LinkedProvider) == "null" {
			patch.LinkedProvider = nil
		} else {
			var linked *string
			if err := json.Unmarshal(body.LinkedProvider, &linked); err != nil {
				return patch, BadRequest("Check what you entered and try again.")
			}
			patch.LinkedProvider = linked
		}
	}
	if body.LocationID != nil || body.LocationChain != nil {
		patch.SetLocation = true
		patch.LocationID = body.LocationID
		patch.LocationChain = body.LocationChain
	}
	return patch, nil
}

func storeErr(err error) error {
	if err == nil {
		return nil
	}
	var se *store.Error
	if !errors.As(err, &se) {
		return InternalError(err)
	}
	switch se.Code {
	case store.CodeInvalid:
		return validationField(se.Msg, se.Field)
	case store.CodeNotFound:
		return NotFound(se.Msg)
	case store.CodeNameTaken, store.CodeAlreadyLinked:
		return conflictField(se.Msg, se.Code, se.Field)
	case store.CodeConflict:
		return Conflict(se.Msg)
	default:
		return InternalError(err)
	}
}

func validationField(msg, field string) error {
	if msg == "" {
		msg = "Check what you entered and try again."
	}
	return &HTTPError{Code: 400, Message: msg, ErrorCode: codeValidation, Field: field}
}

func conflictField(msg, code, field string) error {
	if msg == "" {
		msg = "Someone else just changed this. Refresh and try again."
	}
	return &HTTPError{Code: 409, Message: msg, ErrorCode: code, Field: field}
}
