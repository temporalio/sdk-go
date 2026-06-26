// The WorkflowEvent <-> Nexus link conversion logic in this file is duplicated in
// temporalio/temporal/components/nexusoperations/link_converter.go. Any changes here or there must
// be replicated. This is temporary until the temporal repo updates to the most recent SDK version.

package internal

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
)

const (
	urlSchemeTemporalKey          = "temporal"
	urlPathNamespaceKey           = "namespace"
	urlPathWorkflowIDKey          = "workflowID"
	urlPathOperationIDKey         = "operationID"
	urlPathRunIDKey               = "runID"
	urlPathWorkflowEventTemplate  = "/namespaces/%s/workflows/%s/%s/history"
	urlPathNexusOperationTemplate = "/namespaces/%s/nexus-operations/%s/%s/details"

	linkWorkflowEventReferenceTypeKey = "referenceType"
	linkEventIDKey                    = "eventID"
	linkEventTypeKey                  = "eventType"
	linkRequestIDKey                  = "requestID"
)

var (
	rePatternNamespace   = fmt.Sprintf(`(?P<%s>[^/]+)`, urlPathNamespaceKey)
	rePatternWorkflowID  = fmt.Sprintf(`(?P<%s>[^/]+)`, urlPathWorkflowIDKey)
	rePatternOperationID = fmt.Sprintf(`(?P<%s>[^/]+)`, urlPathOperationIDKey)
	rePatternRunID       = fmt.Sprintf(`(?P<%s>[^/]+)`, urlPathRunIDKey)
	urlPathRE            = regexp.MustCompile(fmt.Sprintf(
		`^/namespaces/%s/workflows/%s/%s/history$`,
		rePatternNamespace,
		rePatternWorkflowID,
		rePatternRunID,
	))
	urlPathNexusOperationRE = regexp.MustCompile(fmt.Sprintf(
		`^/namespaces/%s/nexus-operations/%s/%s/details$`,
		rePatternNamespace,
		rePatternOperationID,
		rePatternRunID,
	))
	eventReferenceType     = string((&commonpb.Link_WorkflowEvent_EventReference{}).ProtoReflect().Descriptor().Name())
	requestIDReferenceType = string((&commonpb.Link_WorkflowEvent_RequestIdReference{}).ProtoReflect().Descriptor().Name())
)

// ConvertLinkWorkflowEventToNexusLink converts a Link_WorkflowEvent type to Nexus Link.
//
// NOTE: Experimental
func ConvertLinkWorkflowEventToNexusLink(we *commonpb.Link_WorkflowEvent) nexus.Link {
	u := &url.URL{
		Scheme: urlSchemeTemporalKey,
		Path:   fmt.Sprintf(urlPathWorkflowEventTemplate, we.GetNamespace(), we.GetWorkflowId(), we.GetRunId()),
		RawPath: fmt.Sprintf(
			urlPathWorkflowEventTemplate,
			url.PathEscape(we.GetNamespace()),
			url.PathEscape(we.GetWorkflowId()),
			url.PathEscape(we.GetRunId()),
		),
	}

	switch ref := we.GetReference().(type) {
	case *commonpb.Link_WorkflowEvent_EventRef:
		u.RawQuery = convertLinkWorkflowEventEventReferenceToURLQuery(ref.EventRef)
	case *commonpb.Link_WorkflowEvent_RequestIdRef:
		u.RawQuery = convertLinkWorkflowEventRequestIdReferenceToURLQuery(ref.RequestIdRef)
	}
	return nexus.Link{
		URL:  u,
		Type: string(we.ProtoReflect().Descriptor().FullName()),
	}
}

// ConvertNexusLinkToLinkWorkflowEvent converts a Nexus Link to Link_WorkflowEvent.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkWorkflowEvent(link nexus.Link) (*commonpb.Link_WorkflowEvent, error) {
	we := &commonpb.Link_WorkflowEvent{}
	if link.Type != string(we.ProtoReflect().Descriptor().FullName()) {
		return nil, fmt.Errorf(
			"cannot parse link type %q to %q",
			link.Type,
			we.ProtoReflect().Descriptor().FullName(),
		)
	}

	if link.URL == nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: empty URL")
	}

	if link.URL.Scheme != urlSchemeTemporalKey {
		return nil, fmt.Errorf(
			"failed to parse link to Link_WorkflowEvent: invalid scheme: %s",
			link.URL.Scheme,
		)
	}

	matches := urlPathRE.FindStringSubmatch(link.URL.EscapedPath())
	if len(matches) != 4 {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: malformed URL path")
	}

	var err error
	we.Namespace, err = url.PathUnescape(matches[urlPathRE.SubexpIndex(urlPathNamespaceKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: %w", err)
	}

	we.WorkflowId, err = url.PathUnescape(matches[urlPathRE.SubexpIndex(urlPathWorkflowIDKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: %w", err)
	}

	we.RunId, err = url.PathUnescape(matches[urlPathRE.SubexpIndex(urlPathRunIDKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: %w", err)
	}

	switch refType := link.URL.Query().Get(linkWorkflowEventReferenceTypeKey); refType {
	case eventReferenceType:
		eventRef, err := convertURLQueryToLinkWorkflowEventEventReference(link.URL.Query())
		if err != nil {
			return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: %w", err)
		}
		we.Reference = &commonpb.Link_WorkflowEvent_EventRef{
			EventRef: eventRef,
		}
	case requestIDReferenceType:
		requestIDRef, err := convertURLQueryToLinkWorkflowEventRequestIdReference(link.URL.Query())
		if err != nil {
			return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent: %w", err)
		}
		we.Reference = &commonpb.Link_WorkflowEvent_RequestIdRef{
			RequestIdRef: requestIDRef,
		}
	default:
		return nil, fmt.Errorf(
			"failed to parse link to Link_WorkflowEvent: unknown reference type: %q",
			refType,
		)
	}

	return we, nil
}

// ConvertLinkNexusOperationToNexusLink converts a Link_NexusOperation type to Nexus Link.
//
// NOTE: Experimental
func ConvertLinkNexusOperationToNexusLink(no *commonpb.Link_NexusOperation) nexus.Link {
	u := &url.URL{
		Scheme: urlSchemeTemporalKey,
		Path:   fmt.Sprintf(urlPathNexusOperationTemplate, no.GetNamespace(), no.GetOperationId(), no.GetRunId()),
		RawPath: fmt.Sprintf(
			urlPathNexusOperationTemplate,
			url.PathEscape(no.GetNamespace()),
			url.PathEscape(no.GetOperationId()),
			url.PathEscape(no.GetRunId()),
		),
	}
	return nexus.Link{
		URL:  u,
		Type: string(no.ProtoReflect().Descriptor().FullName()),
	}
}

// ConvertNexusLinkToLinkNexusOperation converts a Nexus Link to Link_NexusOperation.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkNexusOperation(link nexus.Link) (*commonpb.Link_NexusOperation, error) {
	no := &commonpb.Link_NexusOperation{}
	if link.Type != string(no.ProtoReflect().Descriptor().FullName()) {
		return nil, fmt.Errorf(
			"cannot parse link type %q to %q",
			link.Type,
			no.ProtoReflect().Descriptor().FullName(),
		)
	}

	if link.URL.Scheme != urlSchemeTemporalKey {
		return nil, fmt.Errorf(
			"failed to parse link to Link_NexusOperation: invalid scheme: %s",
			link.URL.Scheme,
		)
	}

	matches := urlPathNexusOperationRE.FindStringSubmatch(link.URL.EscapedPath())
	if len(matches) != 4 {
		return nil, errors.New("failed to parse link to Link_NexusOperation: malformed URL path")
	}

	var err error
	no.Namespace, err = url.PathUnescape(matches[urlPathNexusOperationRE.SubexpIndex(urlPathNamespaceKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_NexusOperation: %w", err)
	}

	no.OperationId, err = url.PathUnescape(matches[urlPathNexusOperationRE.SubexpIndex(urlPathOperationIDKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_NexusOperation: %w", err)
	}

	no.RunId, err = url.PathUnescape(matches[urlPathNexusOperationRE.SubexpIndex(urlPathRunIDKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_NexusOperation: %w", err)
	}

	return no, nil
}

func convertLinkWorkflowEventEventReferenceToURLQuery(eventRef *commonpb.Link_WorkflowEvent_EventReference) string {
	values := url.Values{}
	values.Set(linkWorkflowEventReferenceTypeKey, eventReferenceType)
	if eventRef.GetEventId() > 0 {
		values.Set(linkEventIDKey, strconv.FormatInt(eventRef.GetEventId(), 10))
	}
	values.Set(linkEventTypeKey, eventRef.GetEventType().String())
	return values.Encode()
}

func convertURLQueryToLinkWorkflowEventEventReference(queryValues url.Values) (*commonpb.Link_WorkflowEvent_EventReference, error) {
	var err error
	eventRef := &commonpb.Link_WorkflowEvent_EventReference{}
	eventIDValue := queryValues.Get(linkEventIDKey)
	if eventIDValue != "" {
		eventRef.EventId, err = strconv.ParseInt(queryValues.Get(linkEventIDKey), 10, 64)
		if err != nil {
			return nil, err
		}
	}
	eventRef.EventType, err = enumspb.EventTypeFromString(queryValues.Get(linkEventTypeKey))
	if err != nil {
		return nil, err
	}
	return eventRef, nil
}

func convertLinkWorkflowEventRequestIdReferenceToURLQuery(requestIDRef *commonpb.Link_WorkflowEvent_RequestIdReference) string {
	values := url.Values{}
	values.Set(linkWorkflowEventReferenceTypeKey, requestIDReferenceType)
	values.Set(linkRequestIDKey, requestIDRef.GetRequestId())
	values.Set(linkEventTypeKey, requestIDRef.GetEventType().String())
	return values.Encode()
}

func convertURLQueryToLinkWorkflowEventRequestIdReference(queryValues url.Values) (*commonpb.Link_WorkflowEvent_RequestIdReference, error) {
	var err error
	requestIDRef := &commonpb.Link_WorkflowEvent_RequestIdReference{
		RequestId: queryValues.Get(linkRequestIDKey),
	}
	requestIDRef.EventType, err = enumspb.EventTypeFromString(queryValues.Get(linkEventTypeKey))
	if err != nil {
		return nil, err
	}
	return requestIDRef, nil
}

var (
	workflowEventLinkType  = string((&commonpb.Link_WorkflowEvent{}).ProtoReflect().Descriptor().FullName())
	nexusOperationLinkType = string((&commonpb.Link_NexusOperation{}).ProtoReflect().Descriptor().FullName())
)

// nexusLinkToCommonLink converts a nexus.v1.Link into a common.v1.Link, dispatching on link.Type.
// Returns (nil, false) for any link type not handled here.
func nexusLinkToCommonLink(link *nexuspb.Link) (*commonpb.Link, bool) {
	nexusLink := nexus.Link{Type: link.GetType()}
	if link.GetUrl() != "" {
		u, err := url.Parse(link.GetUrl())
		if err != nil {
			return nil, false
		}
		nexusLink.URL = u
	}
	switch nexusLink.Type {
	case workflowEventLinkType:
		we, err := ConvertNexusLinkToLinkWorkflowEvent(nexusLink)
		if err != nil {
			return nil, false
		}
		return &commonpb.Link{
			Variant: &commonpb.Link_WorkflowEvent_{WorkflowEvent: we},
		}, true
	case nexusOperationLinkType:
		no, err := ConvertNexusLinkToLinkNexusOperation(nexusLink)
		if err != nil {
			return nil, false
		}
		return &commonpb.Link{
			Variant: &commonpb.Link_NexusOperation_{NexusOperation: no},
		}, true
	default:
		return nil, false
	}
}

// commonLinkToNexusLink converts a common.v1.Link into a nexus.v1.Link, dispatching on the link's
// variant. Returns (nil, false) for any variant not handled here.
func commonLinkToNexusLink(link *commonpb.Link) (*nexuspb.Link, bool) {
	switch v := link.GetVariant().(type) {
	case *commonpb.Link_WorkflowEvent_:
		nexusLink := ConvertLinkWorkflowEventToNexusLink(v.WorkflowEvent)
		return &nexuspb.Link{Url: nexusLink.URL.String(), Type: nexusLink.Type}, true
	case *commonpb.Link_NexusOperation_:
		nexusLink := ConvertLinkNexusOperationToNexusLink(v.NexusOperation)
		return &nexuspb.Link{Url: nexusLink.URL.String(), Type: nexusLink.Type}, true
	default:
		return nil, false
	}
}
