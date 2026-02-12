package ado

import "time"

// adoPullRequest maps to the ADO API pull request JSON response.
type adoPullRequest struct {
	PullRequestID int         `json:"pullRequestId"`
	Title         string      `json:"title"`
	Description   string      `json:"description"`
	Status        string      `json:"status"`
	MergeStatus   string      `json:"mergeStatus"`
	SourceRefName string      `json:"sourceRefName"`
	TargetRefName string      `json:"targetRefName"`
	CreatedBy     adoIdentity `json:"createdBy"`
	URL           string      `json:"url"`
	Repository    struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"repository"`
	Links struct {
		Web struct {
			Href string `json:"href"`
		} `json:"web"`
	} `json:"_links"`
}

// adoThread represents a comment thread on a pull request.
type adoThread struct {
	ID            int               `json:"id"`
	Status        any               `json:"status"`
	ThreadContext *adoThreadContext `json:"threadContext,omitempty"`
	Comments      []adoComment      `json:"comments"`
	PublishedDate time.Time         `json:"publishedDate"`
	Properties    map[string]any    `json:"properties,omitempty"`
}

// adoComment represents a single comment within a thread.
type adoComment struct {
	ID              int         `json:"id"`
	Content         string      `json:"content"`
	Author          adoIdentity `json:"author"`
	CommentType     string      `json:"commentType"`
	ParentCommentID int         `json:"parentCommentId,omitempty"`
	PublishedDate   time.Time   `json:"publishedDate"`
}

// adoThreadContext provides file location context for inline comments.
type adoThreadContext struct {
	FilePath       string         `json:"filePath"`
	LeftFileStart  *adoLineOffset `json:"leftFileStart,omitempty"`
	LeftFileEnd    *adoLineOffset `json:"leftFileEnd,omitempty"`
	RightFileStart *adoLineOffset `json:"rightFileStart,omitempty"`
	RightFileEnd   *adoLineOffset `json:"rightFileEnd,omitempty"`
}

// adoLineOffset specifies a line and column offset in a file.
type adoLineOffset struct {
	Line   int `json:"line"`
	Offset int `json:"offset"`
}

// adoBuild represents a build from the ADO builds API.
type adoBuild struct {
	ID           int    `json:"id"`
	BuildNumber  string `json:"buildNumber"`
	Status       string `json:"status"`
	Result       string `json:"result"`
	SourceBranch string `json:"sourceBranch"`
	Definition   struct {
		Name string `json:"name"`
	} `json:"definition"`
	Links struct {
		Web struct {
			Href string `json:"href"`
		} `json:"web"`
	} `json:"_links"`
}

// adoBuildTimeline represents the timeline of a build with task records.
type adoBuildTimeline struct {
	Records []adoTimelineRecord `json:"records"`
}

// adoTimelineRecord represents a single task/step in a build timeline.
type adoTimelineRecord struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Result string `json:"result"`
	Log    *struct {
		ID int `json:"id"`
	} `json:"log,omitempty"`
	Issues []adoIssue `json:"issues,omitempty"`
}

// adoIssue represents an issue reported by a timeline record.
type adoIssue struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// adoIdentity represents a user identity in ADO.
type adoIdentity struct {
	DisplayName string `json:"displayName"`
	ID          string `json:"id"`
	UniqueName  string `json:"uniqueName"`
}

// adoError represents an error response from the ADO API.
type adoError struct {
	Message   string `json:"message"`
	TypeKey   string `json:"typeKey"`
	ErrorCode int    `json:"errorCode"`
}

// adoThreadList is the envelope for the threads list API response.
type adoThreadList struct {
	Value []adoThread `json:"value"`
	Count int         `json:"count"`
}

// adoBuildList is the envelope for the builds list API response.
type adoBuildList struct {
	Value []adoBuild `json:"value"`
	Count int        `json:"count"`
}

// adoConnectionData is the response from the connectiondata endpoint.
type adoConnectionData struct {
	AuthenticatedUser adoIdentity `json:"authenticatedUser"`
}

// adoWorkItemPatchOp represents a single JSON Patch operation for work item creation.
type adoWorkItemPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// adoWorkItem represents a work item response.
type adoWorkItem struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

// adoPullRequestCreate is the request body for creating a new pull request.
type adoPullRequestCreate struct {
	SourceRefName string `json:"sourceRefName"`
	TargetRefName string `json:"targetRefName"`
	Title         string `json:"title"`
	Description   string `json:"description"`
}

// adoPullRequestList is the envelope for the pull requests list API response.
type adoPullRequestList struct {
	Value []adoPullRequest `json:"value"`
	Count int              `json:"count"`
}
