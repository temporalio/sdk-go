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
		validateRamp() error
	}

	// VersioningRampByPercentage sends a proportion of the traffic to the target Build ID.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningRampByPercentage struct {
		// Percentage of traffic with a value in [0,100)
		Percentage float32
	}

	// VersioningAssignmentRule is a BuildID assigment rule for a task queue.
	// Assignment rules only affect new workflows.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningAssignmentRule struct {
		// The BuildID of new workflows affected by this rule.
		TargetBuildID string
		// A strategy for gradual workflow deployment.
		Ramp VersioningRamp
	}

	// VersioningAssignmentRuleWithTimestamp contains an assignment rule annotated
	// by the server with its creation time.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningAssignmentRuleWithTimestamp struct {
		Rule VersioningAssignmentRule
		// The time when the server created this rule.
		CreateTime time.Time
	}

	// VersioningAssignmentRule is a BuildID redirect rule for a task queue.
	// It changes the behavior of currently running workflows and new ones.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningRedirectRule struct {
		SourceBuildID string
		TargetBuildID string
	}

	// VersioningRedirectRuleWithTimestamp contains a redirect rule annotated
	// by the server with its creation time.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningRedirectRuleWithTimestamp struct {
		Rule VersioningRedirectRule
		// The time when the server created this rule.
		CreateTime time.Time
	}

	//VersioningConflictToken is a conflict token to serialize updates.
	// An update with an old token fails with `serviceerror.FailedPrecondition`.
	// The current token can be obtained with [GetWorkerVersioningRules], or returned by a successful [UpdateWorkerVersioningRules].
	// WARNING: Worker versioning-2 is currently experimental
	VersioningConflictToken struct {
		token []byte
	}

	// UpdateWorkerVersioningRulesOptions is the input to [Client.UpdateWorkerVersioningRules].
	// WARNING: Worker versioning-2 is currently experimental
	UpdateWorkerVersioningRulesOptions struct {
		// The task queue to update the versioning rules of.
		TaskQueue string
		// A conflict token to serialize updates.
		ConflictToken VersioningConflictToken
		Operation     VersioningOp
	}

	// VersioningOp is an interface for the different operations that can be
	// performed when updating the worker versioning rules for a task queue.
	//
	// Possible operations are:
	//   - [VersioningOpInsertAssignmentRule]
	//   - [VersioningOpReplaceAssignmentRule]
	//   - [VersioningOpDeleteAssignmentRule]
	//   - [VersioningOpInsertRedirectRule]
	//   - [VersioningOpReplaceRedirectRule]
	//   - [VersioningOpDeleteRedirectRule]
	//   - [VersioningOpCommitBuildID]
	VersioningOp interface {
		validateOp() error
	}

	// VersioningOpInsertAssignmentRule is an operation for UpdateWorkerVersioningRulesOptions
	// that inserts the rule to the list of assignment rules for this Task Queue.
	// The rules are evaluated in order, starting from index 0. The first
	// applicable rule will be applied and the rest will be ignored.
	// By default, the new rule is inserted at the beginning of the list
	// (index 0). If the given index is too larger the rule will be
	// inserted at the end of the list.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpInsertAssignmentRule struct {
		RuleIndex int32
		Rule      VersioningAssignmentRule
	}

	// VersioningOpReplaceAssignmentRule is an operation for UpdateWorkerVersioningRulesOptions
	// that replaces the assignment rule at a given index. By default presence of one
	// unconditional rule, i.e., no hint filter or ramp, is enforced, otherwise
	// the delete operation will be rejected. Set `force` to true to
	// bypass this validation.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpReplaceAssignmentRule struct {
		RuleIndex int32
		Rule      VersioningAssignmentRule
		Force     bool
	}

	// VersioningOpDeleteAssignmentRule is an operation for UpdateWorkerVersioningRulesOptions
	// that deletes the assignment rule at a given index. By default presence of one
	// unconditional rule, i.e., no hint filter or ramp, is enforced, otherwise
	// the delete operation will be rejected. Set `force` to true to
	// bypass this validation.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpDeleteAssignmentRule struct {
		RuleIndex int32
		Force     bool
	}

	// VersioningOpInsertRedirectRule is an operation for UpdateWorkerVersioningRulesOptions
	// that adds the rule to the list of redirect rules for this Task Queue. There
	// can be at most one redirect rule for each distinct Source BuildID.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpInsertRedirectRule struct {
		Rule VersioningRedirectRule
	}

	// VersioningOpReplaceRedirectRule is an operation for UpdateWorkerVersioningRulesOptions
	// that replaces the routing rule with the given source BuildID.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpReplaceRedirectRule struct {
		Rule VersioningRedirectRule
	}

	// VersioningOpDeleteRedirectRule is an operation for UpdateWorkerVersioningRulesOptions
	// that deletes the routing rule with the given source Build ID.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpDeleteRedirectRule struct {
		SourceBuildID string
	}

	// VersioningOpCommitBuildId is an operation for UpdateWorkerVersioningRulesOptions
	// that completes  the rollout of a BuildID and cleanup unnecessary rules possibly
	// created during a gradual rollout. Specifically, this command will make the following changes
	// atomically:
	//  1. Adds an assignment rule (with full ramp) for the target Build ID at
	//     the end of the list.
	//  2. Removes all previously added assignment rules to the given target
	//     Build ID (if any).
	//  3. Removes any fully-ramped assignment rule for other Build IDs.
	//
	// To prevent committing invalid Build IDs, we reject the request if no
	// pollers have been seen recently for this Build ID. Use the `force`
	// option to disable this validation.
	// WARNING: Worker versioning-2 is currently experimental
	VersioningOpCommitBuildID struct {
		TargetBuildID string
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
	if err := uw.Operation.validateOp(); err != nil {
		return nil, err
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
				SourceBuildId: v.SourceBuildID,
			},
		}
	case *VersioningOpCommitBuildID:
		req.Operation = &workflowservice.UpdateWorkerVersioningRulesRequest_CommitBuildId_{
			CommitBuildId: &workflowservice.UpdateWorkerVersioningRulesRequest_CommitBuildId{
				TargetBuildId: v.TargetBuildID,
				Force:         v.Force,
			},
		}
	default:
		return nil, errors.New("converting an invalid operation")
	}

	return req, nil
}

// GetWorkerVersioningOptions is the input to [Client.GetWorkerVersioningRules].
// WARNING: Worker versioning-2 is currently experimental
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

// WorkerVersioningRules is the response for [Client.GetWorkerVersioningRules].
// WARNING: Worker versioning-2 is currently experimental
type WorkerVersioningRules struct {
	AssignmentRules []*VersioningAssignmentRuleWithTimestamp
	RedirectRules   []*VersioningRedirectRuleWithTimestamp
	ConflictToken   VersioningConflictToken
}

func versioningAssignmentRuleToProto(rule *VersioningAssignmentRule) *taskqueuepb.BuildIdAssignmentRule {
	// Assumed `rule` already validated
	result := &taskqueuepb.BuildIdAssignmentRule{
		TargetBuildId: rule.TargetBuildID,
	}

	switch r := rule.Ramp.(type) {
	case *VersioningRampByPercentage:
		result.Ramp = &taskqueuepb.BuildIdAssignmentRule_PercentageRamp{
			PercentageRamp: &taskqueuepb.RampByPercentage{
				RampPercentage: r.Percentage,
			},
		}
	}

	return result
}

func versioningRedirectRuleToProto(rule *VersioningRedirectRule) *taskqueuepb.CompatibleBuildIdRedirectRule {
	// Assumed `rule` already validated
	result := &taskqueuepb.CompatibleBuildIdRedirectRule{
		SourceBuildId: rule.SourceBuildID,
		TargetBuildId: rule.TargetBuildID,
	}

	return result
}

func versioningAssignmentRuleFromProto(rule *taskqueuepb.BuildIdAssignmentRule, timestamp *timestamppb.Timestamp) *VersioningAssignmentRuleWithTimestamp {
	if rule == nil {
		return nil
	}

	result := &VersioningAssignmentRuleWithTimestamp{
		Rule: VersioningAssignmentRule{
			TargetBuildID: rule.GetTargetBuildId(),
		},
	}

	switch r := rule.GetRamp().(type) {
	case *taskqueuepb.BuildIdAssignmentRule_PercentageRamp:
		result.Rule.Ramp = &VersioningRampByPercentage{
			Percentage: r.PercentageRamp.GetRampPercentage(),
		}
	}

	if timestamp != nil {
		result.CreateTime = timestamp.AsTime()
	}
	return result
}

func versioningRedirectRuleFromProto(rule *taskqueuepb.CompatibleBuildIdRedirectRule, timestamp *timestamppb.Timestamp) *VersioningRedirectRuleWithTimestamp {
	if rule == nil {
		return nil
	}

	result := &VersioningRedirectRuleWithTimestamp{
		Rule: VersioningRedirectRule{
			SourceBuildID: rule.GetSourceBuildId(),
			TargetBuildID: rule.GetTargetBuildId(),
		},
	}

	if timestamp != nil {
		result.CreateTime = timestamp.AsTime()
	}
	return result
}

func workerVersioningRulesFromProtoResponse(response *workflowservice.GetWorkerVersioningRulesResponse) *WorkerVersioningRules {
	if response == nil {
		return nil
	}
	aRules := make([]*VersioningAssignmentRuleWithTimestamp, len(response.GetAssignmentRules()))
	for i, s := range response.GetAssignmentRules() {
		aRules[i] = versioningAssignmentRuleFromProto(s.GetRule(), s.GetCreateTime())
	}

	rRules := make([]*VersioningRedirectRuleWithTimestamp, len(response.GetCompatibleRedirectRules()))
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

func (r *VersioningRampByPercentage) validateRamp() error {
	if r.Percentage >= 0.0 && r.Percentage < 100.0 {
		return nil
	} else {
		return errors.New("invalid percentage in `Ramp`, not in [0,100)")
	}
}

func (r *VersioningAssignmentRule) validateRule() error {
	if r.TargetBuildID == "" {
		return errors.New("missing TargetBuildID in assigment rule")
	}
	switch ramp := r.Ramp.(type) {
	case *VersioningRampByPercentage:
		if err := ramp.validateRamp(); err != nil {
			return err
		}
		// Ramp is optional, defaults to "nothing to validate"
	}
	return nil
}

func (r *VersioningRedirectRule) validateRule() error {
	if r.TargetBuildID == "" {
		return errors.New("missing TargetBuildID in redirect rule")
	}
	if r.SourceBuildID == "" {
		return errors.New("missing SourceBuildID in redirect rule")
	}
	return nil
}

func (u *VersioningOpInsertAssignmentRule) validateOp() error  { return u.Rule.validateRule() }
func (u *VersioningOpReplaceAssignmentRule) validateOp() error { return u.Rule.validateRule() }
func (u *VersioningOpDeleteAssignmentRule) validateOp() error  { return nil }
func (u *VersioningOpInsertRedirectRule) validateOp() error    { return u.Rule.validateRule() }
func (u *VersioningOpReplaceRedirectRule) validateOp() error   { return u.Rule.validateRule() }

func (u *VersioningOpDeleteRedirectRule) validateOp() error {
	if u.SourceBuildID == "" {
		return errors.New("missing SourceBuildID")
	}
	return nil
}

func (u *VersioningOpCommitBuildID) validateOp() error {
	if u.TargetBuildID == "" {
		return errors.New("missing TargetBuildID")
	}
	return nil
}
