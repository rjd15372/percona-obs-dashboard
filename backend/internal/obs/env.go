package obs

// Env carries optional pre-fetched project-level OBS data into a task pass.
// A nil *Env (or nil field) means the task fetches its own data per package.
// The worker populates it when a batch job pre-fetched a project-level
// _result response that serves every package in the job.
type Env struct {
	// BuildStates are this package's target build states from a project-level
	// _result call. Nil means BuildStateTask must fetch per-package.
	BuildStates []PackageBuildState
	// RepoStates maps "repo/arch" to the repo publish state ("published",
	// "publishing", …) from the same _result response. Nil means
	// PublishStateTask/BinariesCheckTask must fetch per-package.
	RepoStates map[string]string
}
