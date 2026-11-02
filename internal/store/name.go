package store

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

const maxNameRunes = 40

const settingDefaultStoreID = "default_store_id"

var unicodeNameFold = cases.Fold()

// NameKey is the case-folded trimmed name used for uniqueness.
func NameKey(name string) (string, string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", "", invalid("Enter a store name.", "name")
	}
	if utf8.RuneCountInString(trimmed) > maxNameRunes {
		return "", "", invalid("Store names can be at most 40 characters.", "name")
	}
	return trimmed, unicodeNameFold.String(trimmed), nil
}

func validateKind(kind string) (string, error) {
	k := strings.TrimSpace(kind)
	if k == "" {
		return "physical", nil
	}
	switch k {
	case "physical", "online":
		return k, nil
	default:
		return "", invalid("Check what you entered and try again.", "kind")
	}
}

func validateLinkedProvider(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	p := strings.TrimSpace(*raw)
	if p == "" {
		return nil, nil
	}
	if p != "kroger" {
		return nil, invalid("Check what you entered and try again.", "linkedProvider")
	}
	return &p, nil
}

func validateLocation(linked *string, locationID, locationChain *string) error {
	if linked == nil {
		return nil
	}
	if locationID == nil || strings.TrimSpace(*locationID) == "" {
		return invalid("Choose a store location.", "locationId")
	}
	if locationChain == nil || strings.TrimSpace(*locationChain) == "" {
		return invalid("Choose a store location.", "locationChain")
	}
	return nil
}

func sqlUnique(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "unique") {
		return err
	}
	if strings.Contains(lower, "name_key") {
		return nameTaken()
	}
	if strings.Contains(lower, "linked_provider") {
		return alreadyLinked()
	}
	return fmt.Errorf("could not save the store: %w", err)
}
