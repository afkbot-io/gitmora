package domain

// CommitRange links an observed commit checkpoint to a half-open message interval.
// It is capture evidence, not proof of line authorship or semantic relevance.
type CommitRange struct {
	CommitSHA string `json:"commit_sha"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Basis     string `json:"basis"`
}
