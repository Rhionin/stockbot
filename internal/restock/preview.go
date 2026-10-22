package restock

import "fmt"

// Subject is one item in a category or group preview.
type Subject struct {
	ItemID    string
	Name      string
	GroupName string
	Unit      string
	Input     Input
}

// PreviewLine is one row of a confirm dialog.
type PreviewLine struct {
	ItemID  string `json:"itemId"`
	Name    string `json:"name"`
	Changes bool   `json:"changes"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after"`
	Reason  string `json:"reason,omitempty"`
}

// Preview is the confirm dialog for setting a category or group target.
type Preview struct {
	Title   string        `json:"title"`
	Body    string        `json:"body"`
	Stop    []PreviewLine `json:"stop"`
	Keep    []PreviewLine `json:"keep"`
	Confirm string        `json:"confirm"`
}

// WithCategoryTarget returns in with that category's target replaced.
func WithCategoryTarget(in Input, id, name string, target Target) Input {
	next := make([]CategoryTarget, len(in.Categories))
	copy(next, in.Categories)
	found := false
	for i := range next {
		if next[i].ID == id {
			next[i].Name = name
			next[i].Target = target
			found = true
		}
	}
	if !found {
		next = append(next, CategoryTarget{ID: id, Name: name, Target: target})
	}
	in.Categories = next
	return in
}

// WithGroupTarget returns in with the group target replaced.
func WithGroupTarget(in Input, name string, target Target) Input {
	copied := target
	in.Group = &copied
	in.GroupName = name
	return in
}

// BuildPreview diffs each subject after mutate.
func BuildPreview(name string, proposed Target, subjects []Subject, mutate func(Input) Input) Preview {
	stop := []PreviewLine{}
	keep := []PreviewLine{}
	for _, subject := range subjects {
		before := Resolve(subject.Input)
		after := Resolve(mutate(subject.Input))
		if Same(before.Target, after.Target) {
			keep = append(keep, PreviewLine{
				ItemID: subject.ItemID,
				Name:   subject.Name,
				After:  keepLabel(after.Target),
				Reason: WontChangeReason(after, subject.GroupName),
			})
			continue
		}
		stop = append(stop, PreviewLine{
			ItemID:  subject.ItemID,
			Name:    subject.Name,
			Changes: true,
			Before:  Short(before.Target, ""),
			After:   Short(after.Target, ""),
		})
	}
	preview := Preview{Stop: stop, Keep: keep}
	if proposed.Value == 0 {
		preview.Title = "Stop restocking " + name + "?"
		preview.Body = name + "'s target becomes 0. These items leave the shopping plan. They stay in inventory, and you can still add them to the list by hand."
		preview.Confirm = fmt.Sprintf("Stop restocking %d", len(stop))
		return preview
	}
	phrase := ValuePhrase(proposed, false)
	preview.Title = "Set " + name + " to " + phrase + "?"
	preview.Body = "Items that set their own target stay as they are."
	preview.Confirm = fmt.Sprintf("Change %d", len(stop))
	return preview
}

func keepLabel(target Target) string {
	if target.Kind == KindMonths || target.Value == 0 {
		return Short(target, "")
	}
	return ValuePhrase(target, false)
}
