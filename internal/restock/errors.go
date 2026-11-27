package restock

// InGroupError means an item target cannot be saved because the product is grouped.
type InGroupError struct {
	GroupID   string
	GroupName string
}

func (e *InGroupError) Error() string {
	return "This product is in a group. Change what the group keeps on hand."
}