// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"errors"
	"time"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type (
	// VersioningRamp is an interface for the different strategies of gradual workflow deployments.
	VersioningRamp interface {
		isValidVersioningRamp() bool
	}

	// VersioningRampByPercentage sends a proportion of the traffic to the target Build ID.
	VersioningRampByPercentage struct {
		// Percentage of traffic with a value in [0,100)
		Percentage float32
	}

	// VersioningAssignmentRule is a BuildID assigment rule for a task queue.
	// Assignment rules only affect new workflows.
	VersioningAssignmentRule struct {
		// The BuildID of new workflows affected by this rule.
		TargetBuildId string
		// A strategy for gradual workflow deployment.
		Ramp VersioningRamp
		// An optional registration time set by the server.
		Timestamp *time.Time
	}

	// VersioningAssignmentRule is a BuildID redirect rule for a task queue.
	// It changes the behavior of currently running workflows and new ones.
	VersioningRedirectRule struct {
		SourceBuildId string
		TargetBuildId string
		// An optional registration time set by the server.
		Timestamp *time.Time
	}

	//VersioningConflictToken is a conflict token to serialize updates.
	// An update with an old token fails with `serviceerror.FailedPrecondition`.
	// The current token can be obtained with [GetWorkerVersioningRules], or returned by a successful [UpdateWorkerVersioningRules].
	VersioningConflictToken struct {
		token []byte
	}

	// UpdateWorkerVersioningRulesOptions is the input to Client.UpdateWorkerVersioningRules.
	UpdateWorkerVersioningRulesOptions struct {
		// The task queue to update the versioning rules of.
		TaskQueue string
		// A conflict token to serialize updates.
		ConflictToken VersioningConflictToken
		Operation     UpdateVersioningOp
	}

	// UpdateVersioningOp is an interface for the different operations that can be
	// performed when updating the worker versioning rules for a task queue.
	//
	// Possible operations are:
	//   - VersioningOpInsertAssignmentRule
	//   - VersioningOpReplaceAssignmentRule
	//   - VersioningOpDeleteAssignmentRule
	//   - VersioningOpInsertRedirectRule
	//   - VersioningOpReplaceRedirectRule
	//   - VersioningOpDeleteRedirectRule
	//   - VersioningOpCommitBuildId
	UpdateVersioningOp interface {
		isValidUpdateVersioningOp() bool
	}
	VersioningOpInsertAssignmentRule struct {
		RuleIndex int32
		Rule      VersioningAssignmentRule
	}
	VersioningOpReplaceAssignmentRule struct {
		RuleIndex int32
		Rule      VersioningAssignmentRule
		Force     bool
	}
	VersioningOpDeleteAssignmentRule struct {
		RuleIndex int32
		Force     bool
	}
	VersioningOpInsertRedirectRule struct {
		Rule VersioningRedirectRule
	}
	VersioningOpReplaceRedirectRule struct {
		Rule VersioningRedirectRule
	}
	VersioningOpDeleteRedirectRule struct {
		SourceBuildId string
	}
	VersioningOpCommitBuildId struct {
		TargetBuildId string
		Force         bool
	}
)

func (uw *UpdateWorkerVersioningRulesOptions) validateAndConvertToProto(namespace string) (*workflowservice.UpdateWorkerVersioningRulesRequest, error) {
	if namespace == "" {
		return nil, errors.New("missing namespace argument")
	}
	if uw.TaskQueue == "" {
		return nil, errors.New("missing TaskQueue field")
	}
	if !uw.Operation.isValidUpdateVersioningOp() {
		return nil, errors.New("invalid Operation BuildID field")
	}
	req := &workflowservice.UpdateWorkerVersioningRulesRequest{
		Namespace:     namespace,
		TaskQueue:     uw.TaskQueue,
		ConflictToken: uw.ConflictToken.token,
	}

	switch v := uw.Operation.(type) {
	case *VersioningOpInsertAssignmentRule:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_InsertAssignmentRule{
			InsertAssignmentRule: &workflowservice.UpdateWorkerVersioningRulesRequest_InsertBuildIdAssignmentRule{
				RuleIndex: v.RuleIndex,
				Rule:      versioningAssignmentRuleToProto(&v.Rule),
			},
		}
	case *VersioningOpReplaceAssignmentRule:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_ReplaceAssignmentRule{
			ReplaceAssignmentRule: &workflowservice.UpdateWorkerVersioningRulesRequest_ReplaceBuildIdAssignmentRule{
				RuleIndex: v.RuleIndex,
				Rule:      versioningAssignmentRuleToProto(&v.Rule),
				Force:     v.Force,
			},
		}
	case *VersioningOpDeleteAssignmentRule:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_DeleteAssignmentRule{
			DeleteAssignmentRule: &workflowservice.UpdateWorkerVersioningRulesRequest_DeleteBuildIdAssignmentRule{
				RuleIndex: v.RuleIndex,
				Force:     v.Force,
			},
		}
	case *VersioningOpInsertRedirectRule:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_InsertCompatibleRedirectRule{
			InsertCompatibleRedirectRule: &workflowservice.UpdateWorkerVersioningRulesRequest_AddCompatibleBuildIdRedirectRule{
				Rule: versioningRedirectRuleToProto(&v.Rule),
			},
		}
	case *VersioningOpReplaceRedirectRule:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_ReplaceCompatibleRedirectRule{
			ReplaceCompatibleRedirectRule: &workflowservice.UpdateWorkerVersioningRulesRequest_ReplaceCompatibleBuildIdRedirectRule{
				Rule: versioningRedirectRuleToProto(&v.Rule),
			},
		}
	case *VersioningOpDeleteRedirectRule:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_DeleteCompatibleRedirectRule{
			DeleteCompatibleRedirectRule: &workflowservice.UpdateWorkerVersioningRulesRequest_DeleteCompatibleBuildIdRedirectRule{
				SourceBuildId: v.SourceBuildId,
			},
		}
	case *VersioningOpCommitBuildId:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_CommitBuildId_{
			CommitBuildId: &workflowservice.UpdateWorkerVersioningRulesRequest_CommitBuildId{
				TargetBuildId: v.TargetBuildId,
				Force:         v.Force,
			},
		}
	}

	return req, nil
}

type GetWorkerVersioningOptions struct {
	// The task queue to get the versioning rules from.
	TaskQueue string
}

func (gw *GetWorkerVersioningOptions) validateAndConvertToProto(namespace string) (*workflowservice.GetWorkerVersioningRulesRequest, error) {
	if namespace == "" {
		return nil, errors.New("missing namespace argument")
	}

	if gw.TaskQueue == "" {
		return nil, errors.New("missing  TaskQueue field")
	}
	req := &workflowservice.GetWorkerVersioningRulesRequest{
		Namespace: namespace,
		TaskQueue: gw.TaskQueue,
	}

	return req, nil
}

type WorkerVersioningRules struct {
	AssignmentRules []*VersioningAssignmentRule
	RedirectRules   []*VersioningRedirectRule
	ConflictToken   VersioningConflictToken
}

func versioningAssignmentRuleToProto(rule *VersioningAssignmentRule) *taskqueuepb.BuildIdAssignmentRule {
	// Assumed `rule` already validated
	result := &taskqueuepb.BuildIdAssignmentRule{
		TargetBuildId: rule.TargetBuildId,
	}

	switch r := rule.Ramp.(type) {
	case *VersioningRampByPercentage:
		result.Ramp = &taskqueuepb.BuildIdAssignmentRule_PercentageRamp{
			PercentageRamp: &taskqueuepb.RampByPercentage{
				RampPercentage: r.Percentage,
			},
		}
	}
	// Ignore `rule.Timestamp`

	return result
}

func versioningRedirectRuleToProto(rule *VersioningRedirectRule) *taskqueuepb.CompatibleBuildIdRedirectRule {
	// Assumed `rule` already validated
	result := &taskqueuepb.CompatibleBuildIdRedirectRule{
		SourceBuildId: rule.SourceBuildId,
		TargetBuildId: rule.TargetBuildId,
	}
	// Ignore `rule.Timestamp`

	return result
}

func versioningAssignmentRuleFromProto(rule *taskqueuepb.BuildIdAssignmentRule, timestamp *timestamppb.Timestamp) *VersioningAssignmentRule {
	if rule == nil {
		return nil
	}

	result := &VersioningAssignmentRule{
		TargetBuildId: rule.GetTargetBuildId(),
	}

	switch r := rule.GetRamp().(type) {
	case *taskqueuepb.BuildIdAssignmentRule_PercentageRamp:
		result.Ramp = &VersioningRampByPercentage{
			Percentage: r.PercentageRamp.GetRampPercentage(),
		}
	}

	if timestamp != nil {
		t := timestamp.AsTime()
		result.Timestamp = &t
	}
	return result
}

func versioningRedirectRuleFromProto(rule *taskqueuepb.CompatibleBuildIdRedirectRule, timestamp *timestamppb.Timestamp) *VersioningRedirectRule {
	if rule == nil {
		return nil
	}

	result := &VersioningRedirectRule{
		SourceBuildId: rule.GetSourceBuildId(),
		TargetBuildId: rule.GetTargetBuildId(),
	}

	if timestamp != nil {
		t := timestamp.AsTime()
		result.Timestamp = &t
	}
	return result
}

func workerVersioningRulesFromProtoResponse(response *workflowservice.GetWorkerVersioningRulesResponse) *WorkerVersioningRules {
	if response == nil {
		return nil
	}
	aRules := make([]*VersioningAssignmentRule, len(response.GetAssignmentRules()))
	for i, s := range response.GetAssignmentRules() {
		aRules[i] = versioningAssignmentRuleFromProto(s.GetRule(), s.GetCreateTime())
	}

	rRules := make([]*VersioningRedirectRule, len(response.GetCompatibleRedirectRules()))
	for i, s := range response.GetCompatibleRedirectRules() {
		rRules[i] = versioningRedirectRuleFromProto(s.GetRule(), s.GetCreateTime())
	}

	conflictToken := VersioningConflictToken{
		token: response.GetConflictToken(),
	}
	return &WorkerVersioningRules{
		AssignmentRules: aRules,
		RedirectRules:   rRules,
		ConflictToken:   conflictToken,
	}
}

func workerVersioningConflictTokenFromProtoResponse(response *workflowservice.UpdateWorkerVersioningRulesResponse) VersioningConflictToken {
	if response == nil {
		return VersioningConflictToken{}
	}
	return VersioningConflictToken{
		token: response.GetConflictToken(),
	}
}

func (r *VersioningRampByPercentage) isValidVersioningRamp() bool { return true }

func (r *VersioningAssignmentRule) isValid() bool { return r.TargetBuildId != "" }
func (r *VersioningRedirectRule) isValid() bool {
	return r.TargetBuildId != "" && r.SourceBuildId != ""
}

func (u *VersioningOpInsertAssignmentRule) isValidUpdateVersioningOp() bool  { return u.Rule.isValid() }
func (u *VersioningOpReplaceAssignmentRule) isValidUpdateVersioningOp() bool { return u.Rule.isValid() }
func (u *VersioningOpDeleteAssignmentRule) isValidUpdateVersioningOp() bool  { return true }
func (u *VersioningOpInsertRedirectRule) isValidUpdateVersioningOp() bool    { return u.Rule.isValid() }
func (u *VersioningOpReplaceRedirectRule) isValidUpdateVersioningOp() bool   { return u.Rule.isValid() }
func (u *VersioningOpDeleteRedirectRule) isValidUpdateVersioningOp() bool {
	return u.SourceBuildId != ""
}
func (u *VersioningOpCommitBuildId) isValidUpdateVersioningOp() bool { return u.TargetBuildId != "" }
