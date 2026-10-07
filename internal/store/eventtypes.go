package store

// Event type strings carried in events.type. They form the tagged union the
// browser SPA and the Claude-side stream consumers switch on.
const (
	EventCommentCreated      = "comment.created"
	EventCommentUpdated      = "comment.updated"
	EventCommentResolved     = "comment.resolved"
	EventClaudeQuestion      = "claude.question"
	EventClaudeAsk           = "claude.ask"
	EventClaudeClarification = "claude.clarification"
	EventSubmit              = "submit"
	EventFileStates          = "file.states"
	EventVersionCreated      = "version.created"
	EventAIRequestCreated    = "ai.request.created"
	EventAIRequestUpdated    = "ai.request.updated"
	EventOrganizationUpdated = "organization.updated"
	EventAnnotationsUpdated  = "annotations.updated"
	EventChannelChanged      = "channel.changed"
	EventStatusChanged       = "status.changed"
	EventPRUpdated           = "pr.updated"
	EventCommentSynced       = "comment.synced"
)

// Origins recorded in events.origin.
const (
	OriginUser   = "user"
	OriginClaude = "claude"
	OriginSystem = "system"
)

// Authors recorded in comments.author and replies.origin. AuthorRemote is a
// GitHub user who is neither the viewer nor the cc-review app's bot.
const (
	AuthorUser   = "user"
	AuthorClaude = "claude"
	AuthorRemote = "remote"
)

// Sync states recorded in comments.sync_state and replies.sync_state. A local
// review's rows stay SyncLocal and never reach GitHub.
const (
	SyncLocal   = "local"
	SyncPosting = "posting"
	SyncSynced  = "synced"
	SyncFailed  = "failed"
)

// Review kinds recorded in review_meta.kind.
const (
	ReviewKindLocal = "local"
	ReviewKindPR    = "pr"
)
