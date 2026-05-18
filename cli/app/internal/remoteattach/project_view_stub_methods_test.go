package remoteattach

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

func (*projectViewRemoteStub) GetProjectEdit(context.Context, serverapi.ProjectEditGetRequest) (serverapi.ProjectEditGetResponse, error) {
	return serverapi.ProjectEditGetResponse{}, errors.New("unexpected GetProjectEdit call")
}

func (*projectViewRemoteStub) UpdateProject(context.Context, serverapi.ProjectUpdateRequest) (serverapi.ProjectUpdateResponse, error) {
	return serverapi.ProjectUpdateResponse{}, errors.New("unexpected UpdateProject call")
}

func (*projectViewRemoteStub) SetDefaultWorkspace(context.Context, serverapi.ProjectDefaultWorkspaceSetRequest) (serverapi.ProjectDefaultWorkspaceSetResponse, error) {
	return serverapi.ProjectDefaultWorkspaceSetResponse{}, errors.New("unexpected SetDefaultWorkspace call")
}

func (*projectViewRemoteStub) UnlinkWorkspaceFromProject(context.Context, serverapi.ProjectWorkspaceUnlinkRequest) (serverapi.ProjectWorkspaceUnlinkResponse, error) {
	return serverapi.ProjectWorkspaceUnlinkResponse{}, errors.New("unexpected UnlinkWorkspaceFromProject call")
}
