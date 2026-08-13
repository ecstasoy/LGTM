package api

// Stable machine-readable error codes. The frontend renders copy from its own dictionary keyed by these;
// the human-readable "error" field stays as-is for curl and non-browser consumers.
const (
	CodeNotLoggedIn          = "not_logged_in"
	CodeOAuthNotConfigured   = "oauth_not_configured"
	CodeUnknownModel         = "unknown_model"
	CodePRNotFound           = "pr_not_found"
	CodeGitHubForbidden      = "github_forbidden"
	CodeNoPushPermission     = "no_push_permission"
	CodeNoCommentPermission  = "no_comment_permission"
	CodeSuggestionNoAnchor   = "suggestion_missing_anchor"
	CodeSuggestionNoPatch    = "suggestion_missing_patch"
	CodeEmptyPR              = "empty_pr"
	CodeHistoryLoginRequired = "history_login_required"
	CodeNotReviewOwner       = "not_review_owner"
)
