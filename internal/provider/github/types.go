package github

// prIdentifier holds parsed components of a GitHub PR reference.
type prIdentifier struct {
	Owner  string
	Repo   string
	Number int
}
