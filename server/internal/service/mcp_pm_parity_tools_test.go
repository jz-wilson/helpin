package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/helpin-ai/helpin/server/internal/authorization"
	"github.com/helpin-ai/helpin/server/internal/model"
)

func TestArchiveAndRestoreTask(t *testing.T) {
	for _, tt := range []struct {
		tool string
		want bool
	}{{tool: "archive_task", want: true}, {tool: "restore_task", want: false}} {
		t.Run(tt.tool, func(t *testing.T) {
			service, principal, actor, tasks, _ := setupMCPPMToolTest()
			if _, err := service.executeSpecialMCPTool(context.Background(), principal, actor, tt.tool,
				json.RawMessage(`{"task_id":"task-1"}`)); err != nil {
				t.Fatalf("%s error = %v", tt.tool, err)
			}
			if tasks.updateID != "task-1" || tasks.updateRequest.Archived == nil || *tasks.updateRequest.Archived != tt.want {
				t.Fatalf("update = %s %#v", tasks.updateID, tasks.updateRequest)
			}
		})
	}
}

func TestArchiveTaskHidesOtherWorkspaces(t *testing.T) {
	service, principal, actor, tasks, _ := setupMCPPMToolTest()
	tasks.tasks["task-1"].Task.WorkspaceID = "workspace-other"
	_, err := service.executeSpecialMCPTool(context.Background(), principal, actor, "archive_task",
		json.RawMessage(`{"task_id":"task-1"}`))
	if !errors.Is(err, ErrMCPNotFound) || tasks.updateID != "" {
		t.Fatalf("error = %v, update = %q", err, tasks.updateID)
	}
}

func TestListTaskCommentsExposesOnlyPublicFields(t *testing.T) {
	service, principal, actor, _, _ := setupMCPPMToolTest()
	service.pmComments = fakeMCPPMCommentLister{
		{
			Comment: model.PMComment{ID: "c-1", AuthorID: "user-2", Body: "Looks good"},
			Author:  model.User{ID: "user-2", FullName: "Sam Lee", Email: "sam@example.com"},
			Replies: []model.CommentWithAuthor{{
				Comment: model.PMComment{ID: "c-2", AuthorID: "user-1", Body: "Thanks"},
				Author:  model.User{ID: "user-1", FullName: "Ava"},
			}},
		},
	}
	result, err := service.executeSpecialMCPTool(context.Background(), principal, actor, "list_task_comments",
		json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil {
		t.Fatalf("list_task_comments error = %v", err)
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonContains(encoded, `"author_name":"Sam Lee"`) || !jsonContains(encoded, `"body":"Thanks"`) {
		t.Fatalf("comments = %s", encoded)
	}
	if jsonContains(encoded, "sam@example.com") {
		t.Fatalf("comments leaked author email: %s", encoded)
	}
}

type fakeMCPPMCommentLister []model.CommentWithAuthor

func (f fakeMCPPMCommentLister) List(context.Context, string, string) ([]model.CommentWithAuthor, error) {
	return f, nil
}

func jsonContains(encoded []byte, fragment string) bool {
	return strings.Contains(string(encoded), fragment)
}

func TestResolveMCPTaskKeyArgument(t *testing.T) {
	commands := NewInternalCommandService(nil, nil, nil, nil, nil, nil, nil, nil)
	service := &MCPService{commands: commands}
	principal := &model.MCPPrincipal{WorkspaceID: "workspace-1", UserID: "user-1"}
	actor := &authorization.Actor{WorkspaceID: "workspace-1", UserID: "user-1", Role: "admin"}
	for _, arguments := range []string{
		`{"task_id":"4c7bf8bc-5be1-7abd-8eed-b26ab273d21e"}`,
		`{"document_id":"HEL-120"}`,
		`{"task_id":"not-a-key"}`,
	} {
		got, err := service.resolveMCPTaskKeyArgument(context.Background(), principal, actor, json.RawMessage(arguments))
		if err != nil || string(got) != arguments {
			t.Fatalf("resolve(%s) = %s, %v; want unchanged", arguments, got, err)
		}
	}
	_, err := service.resolveMCPTaskKeyArgument(context.Background(), principal, actor, json.RawMessage(`{"task_id":"hel-120"}`))
	if !errors.Is(err, ErrMCPNotFound) {
		t.Fatalf("unresolvable key error = %v, want ErrMCPNotFound", err)
	}
}

type fakeMCPPMCommentDeleter struct {
	comments  map[string]*model.PMComment
	deletedID string
	isAdmin   bool
	actorID   string
	workspace string
}

func (f *fakeMCPPMCommentDeleter) Get(_ context.Context, id string) (*model.PMComment, error) {
	return f.comments[id], nil
}

func (f *fakeMCPPMCommentDeleter) Delete(_ context.Context, id, actorID string, isAdmin bool, workspaceID string) error {
	f.deletedID, f.actorID, f.isAdmin, f.workspace = id, actorID, isAdmin, workspaceID
	return nil
}

func setupDeleteTaskCommentTest() (*MCPService, *model.MCPPrincipal, *authorization.Actor, *fakeMCPPMCommentDeleter, *fakeMCPPMTaskService) {
	service, principal, actor, tasks, _ := setupMCPPMToolTest()
	tasks.tasks["task-other"] = &model.TaskDetail{Task: model.PMTask{ID: "task-other", WorkspaceID: "workspace-other"}}
	comments := &fakeMCPPMCommentDeleter{comments: map[string]*model.PMComment{
		"c-own":     {ID: "c-own", EntityType: "task", EntityID: "task-1", AuthorID: "user-1"},
		"c-theirs":  {ID: "c-theirs", EntityType: "task", EntityID: "task-1", AuthorID: "user-2"},
		"c-foreign": {ID: "c-foreign", EntityType: "task", EntityID: "task-other", AuthorID: "user-1"},
		"c-epic":    {ID: "c-epic", EntityType: "epic", EntityID: "epic-1", AuthorID: "user-1"},
	}}
	service.pmCommentDeleter = comments
	return service, principal, actor, comments, tasks
}

func TestDeleteTaskCommentDeletesOwnComment(t *testing.T) {
	service, principal, actor, comments, _ := setupDeleteTaskCommentTest()
	result, err := service.executeSpecialMCPTool(context.Background(), principal, actor, "delete_task_comment",
		json.RawMessage(`{"comment_id":"c-own"}`))
	if err != nil {
		t.Fatalf("delete_task_comment error = %v", err)
	}
	if comments.deletedID != "c-own" || comments.isAdmin || comments.actorID != "user-1" || comments.workspace != "workspace-1" {
		t.Fatalf("delete call = %+v", comments)
	}
	encoded, _ := json.Marshal(result.Data)
	if !jsonContains(encoded, `"comment_id":"c-own"`) || !jsonContains(encoded, `"task_id":"task-1"`) {
		t.Fatalf("result = %s", encoded)
	}
}

func TestDeleteTaskCommentRejectsOtherAuthors(t *testing.T) {
	service, principal, actor, comments, _ := setupDeleteTaskCommentTest()
	_, err := service.executeSpecialMCPTool(context.Background(), principal, actor, "delete_task_comment",
		json.RawMessage(`{"comment_id":"c-theirs"}`))
	if !errors.Is(err, ErrMCPForbidden) || comments.deletedID != "" {
		t.Fatalf("error = %v, deleted = %q", err, comments.deletedID)
	}
}

func TestDeleteTaskCommentHidesMissingAndForeignComments(t *testing.T) {
	for _, id := range []string{"missing", "c-foreign", "c-epic"} {
		t.Run(id, func(t *testing.T) {
			service, principal, actor, comments, _ := setupDeleteTaskCommentTest()
			_, err := service.executeSpecialMCPTool(context.Background(), principal, actor, "delete_task_comment",
				json.RawMessage(`{"comment_id":"`+id+`"}`))
			if !errors.Is(err, ErrMCPNotFound) || comments.deletedID != "" {
				t.Fatalf("error = %v, deleted = %q", err, comments.deletedID)
			}
		})
	}
}

func TestDeleteTaskCommentToolDefinition(t *testing.T) {
	var tool MCPToolDefinition
	for _, candidate := range pmParityMCPToolDefinitions() {
		if candidate.Name == "delete_task_comment" {
			tool = candidate
		}
	}
	if tool.Scope != MCPScopePMWrite || tool.Permission != authorization.PermPMEdit || !tool.Mutating || !tool.Destructive {
		t.Fatalf("definition = %+v", tool)
	}
	if _, ok := tool.InputSchema["properties"].(map[string]any)["idempotency_key"]; !ok {
		t.Fatalf("schema lacks idempotency_key: %v", tool.InputSchema)
	}
}
