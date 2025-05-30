package internal

import (
	"fmt"
	"strings"

	deploymentpb "go.temporal.io/api/deployment/v1"
)

// Represents the version of a specific worker deployment.
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/client.WorkerDeploymentVersion], [go.temporal.io/sdk/worker.WorkerDeploymentVersion]
type WorkerDeploymentVersion struct {
	// The name of the deployment this worker version belongs to
	DeploymentName string
	// The build id specific to this worker
	BuildId string
}

func workerDeploymentVersionFromProto(wd *deploymentpb.WorkerDeploymentVersion) WorkerDeploymentVersion {
	return WorkerDeploymentVersion{
		DeploymentName: wd.DeploymentName,
		BuildId:        wd.BuildId,
	}
}

func (wd *WorkerDeploymentVersion) toProto() *deploymentpb.WorkerDeploymentVersion {
	return &deploymentpb.WorkerDeploymentVersion{
		DeploymentName: wd.DeploymentName,
		BuildId:        wd.BuildId,
	}
}

func (wd *WorkerDeploymentVersion) ToCanonicalString() string {
	return fmt.Sprintf("%s.%s", wd.DeploymentName, wd.BuildId)
}

func workerDeploymentVersionFromString(version string) *WorkerDeploymentVersion {
	if splitVersion := strings.SplitN(version, ".", 2); len(splitVersion) == 2 {
		return &WorkerDeploymentVersion{
			DeploymentName: splitVersion[0],
			BuildId:        splitVersion[1],
		}
	}
	return nil
}

func workerDeploymentVersionFromProtoOrString(wd *deploymentpb.WorkerDeploymentVersion, fallback string) *WorkerDeploymentVersion {
	if wd == nil {
		return workerDeploymentVersionFromString(fallback)
	}
	return &WorkerDeploymentVersion{
		DeploymentName: wd.DeploymentName,
		BuildId:        wd.BuildId,
	}
}
