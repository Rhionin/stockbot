package restock

import (
	"fmt"
	"math"
	"strconv"
)

// ChainCell is one column of the Item / Group / Category / Default strip.
type ChainCell struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Win    bool   `json:"win"`
	Struck bool   `json:"struck"`
}

// SourceLabel names who set the target, in plain words.
func SourceLabel(res Resolution) string {
	switch res.Level {
	case LevelItem:
		return "Set on this item"
	case LevelGroup:
		return "From " + res.Name + " group"
	case LevelCategory:
		return "From " + res.Name
	default:
		return "Household default"
	}
}

// ValuePhrase is the target as a person would say it.
// each is for a category amount, which applies per item.
func ValuePhrase(target Target, each bool) string {
	if target.Value == 0 {
		return "Don't restock"
	}
	if target.Kind == KindMonths {
		n := int(math.Round(target.Value))
		if n == 1 {
			return "1 month"
		}
		return fmt.Sprintf("%d months", n)
	}
	n := int(math.Round(target.Value))
	if each {
		if n == 1 {
			return "Keep 1 each"
		}
		return fmt.Sprintf("Keep %d each", n)
	}
	return fmt.Sprintf("Keep %d", n)
}

// Short is the compact form used in the strip and the preview arrow.
func Short(target Target, unit string) string {
	if target.Value == 0 {
		return "0"
	}
	if target.Kind == KindMonths {
		return fmt.Sprintf("%d mo", int(math.Round(target.Value)))
	}
	n := formatNumber(target.Value)
	if unit != "" {
		return n + " " + unit
	}
	return "Keep " + n
}

// QuietLine is the extra line on a collapsed row.
// It is set only when the item itself has a target, or the target is 0.
func QuietLine(res Resolution) string {
	if res.Target.Value == 0 {
		switch res.Level {
		case LevelItem:
			return "Not restocked · this item"
		case LevelHousehold:
			return "Not restocked · household default"
		default:
			return "Not restocked · " + res.Name
		}
	}
	if res.Level != LevelItem {
		return ""
	}
	if res.Target.Kind == KindAmount {
		return fmt.Sprintf("Keep %d · set on this item", int(math.Round(res.Target.Value)))
	}
	return ValuePhrase(res.Target, false) + " · set on this item"
}

// RowSource is the line under a category member.
func RowSource(res Resolution) string {
	if res.Target.Value == 0 {
		return QuietLine(res)
	}
	phrase := ValuePhrase(res.Target, false)
	switch res.Level {
	case LevelItem:
		return phrase + ", set on this item"
	case LevelGroup:
		return phrase + ", from " + res.Name + " group"
	case LevelCategory:
		return phrase + ", from " + res.Name
	default:
		return phrase + ", household default"
	}
}

// WontChangeReason explains why a preview row stays put.
// groupName is the group the item belongs to, when it has one.
func WontChangeReason(res Resolution, groupName string) string {
	switch res.Level {
	case LevelItem:
		if groupName != "" {
			return "Set on this item (in " + groupName + " group)"
		}
		return "Set on this item"
	case LevelGroup:
		return "From " + res.Name + " group"
	case LevelCategory:
		if len(res.Shadowed) > 0 {
			if len(res.Shadowed) == 1 {
				return "From " + res.Name + ", the higher of its two categories"
			}
			return "From " + res.Name + ", the higher of its categories"
		}
		return "From " + res.Name
	default:
		return "Household default"
	}
}

// UseAction is the menu line that clears the current target.
// ok is false at the household default, which has nothing broader to use.
func UseAction(res Resolution) (label, hint string, ok bool) {
	if res.Next == nil {
		return "", "", false
	}
	next := *res.Next
	hint = hintValue(next.Target)
	switch next.Level {
	case LevelGroup:
		return "Use group target", next.Name + " · " + hint, true
	case LevelCategory:
		return "Use category target", next.Name + " · " + hint, true
	default:
		return "Use household default", hint, true
	}
}

func hintValue(target Target) string {
	if target.Value == 0 {
		return "Don't restock"
	}
	if target.Kind == KindMonths {
		return ValuePhrase(target, false)
	}
	return ValuePhrase(target, false)
}

// Chain builds the four-column strip. unit is the item's unit for an amount
// set on the item ("bags"). Broader columns that lost are struck.
func Chain(in Input, res Resolution, unit string) []ChainCell {
	itemValue := "—"
	if in.Item != nil {
		itemValue = Short(*in.Item, unit)
	}
	groupValue := "—"
	if in.Group != nil {
		groupValue = Short(*in.Group, "")
	}
	categoryValue := "—"
	if len(in.Categories) > 0 {
		best := in.Categories[0].Target
		for _, category := range in.Categories[1:] {
			best = Higher(best, category.Target)
		}
		categoryValue = Short(best, "")
	}
	cells := []ChainCell{
		{Key: "item", Label: "Item", Value: itemValue, Win: res.Level == LevelItem},
		{Key: "group", Label: "Group", Value: groupValue, Win: res.Level == LevelGroup},
		{Key: "category", Label: "Category", Value: categoryValue, Win: res.Level == LevelCategory},
		{Key: "household", Label: "Default", Value: Short(in.Household, ""), Win: res.Level == LevelHousehold},
	}
	rank := map[Level]int{LevelItem: 0, LevelGroup: 1, LevelCategory: 2, LevelHousehold: 3}
	order := []Level{LevelItem, LevelGroup, LevelCategory, LevelHousehold}
	win := rank[res.Level]
	for i, level := range order {
		if rank[level] > win && cells[i].Value != "—" {
			cells[i].Struck = true
		}
	}
	return cells
}

// DontRestockDetail is the undo note after one item is set to 0.
func DontRestockDetail(before Resolution) string {
	if before.Level == LevelCategory || before.Level == LevelGroup {
		return "Its target is now 0. " + before.Name + " is unchanged."
	}
	return "Its target is now 0."
}

func formatNumber(value float64) string {
	if value == math.Trunc(value) {
		return strconv.Itoa(int(value))
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
