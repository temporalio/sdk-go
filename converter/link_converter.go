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

package converter

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
)

const (
	urlSchemeTemporalKey = "temporal"
	urlPathNamespaceKey  = "namespace"
	urlPathWorkflowIDKey = "workflowID"
	urlPathRunIDKey      = "runID"
	urlPathTemplate      = "/namespaces/%s/workflows/%s/%s/history"
	urlTemplate          = "temporal://" + urlPathTemplate

	linkWorkflowEventReferenceTypeKey = "referenceType"
	linkEventReferenceEventIDKey      = "eventID"
	linkEventReferenceEventTypeKey    = "eventType"
)

var (
	rePatternNamespace  = fmt.Sprintf(`(?P<%s>[a-z0-9-.]+)`, urlPathNamespaceKey)
	rePatternWorkflowID = fmt.Sprintf(`(?P<%s>[^/]+)`, urlPathWorkflowIDKey)
	rePatternRunID      = fmt.Sprintf(`(?P<%s>[a-zA-Z0-9-]+)`, urlPathRunIDKey)
	urlPathRE           = regexp.MustCompile(fmt.Sprintf(
		`^/+namespaces/+%s/+workflows/+%s/+%s/+history/*$`,
		rePatternNamespace,
		rePatternWorkflowID,
		rePatternRunID,
	))
)

func ConvertLinkWorkflowEventToNexusLink(we *commonpb.Link_WorkflowEvent) nexus.Link {
	u := &url.URL{
		Scheme: urlSchemeTemporalKey,
		Path:   fmt.Sprintf(urlPathTemplate, we.GetNamespace(), we.GetWorkflowId(), we.GetRunId()),
		RawPath: fmt.Sprintf(
			urlPathTemplate,
			url.PathEscape(we.GetNamespace()),
			url.PathEscape(we.GetWorkflowId()),
			url.PathEscape(we.GetRunId()),
		),
	}

	switch ref := we.GetReference().(type) {
	case *commonpb.Link_WorkflowEvent_EventRef:
		u.RawQuery = convertLinkWorkflowEventEventReferenceToURLQuery(ref.EventRef)
	}
	return nexus.Link{
		URL:  u,
		Type: string(we.ProtoReflect().Descriptor().FullName()),
	}
}

func ConvertNexusLinkToLinkWorkflowEvent(link nexus.Link) (*commonpb.Link_WorkflowEvent, error) {
	we := &commonpb.Link_WorkflowEvent{}
	if link.Type != string(we.ProtoReflect().Descriptor().FullName()) {
		return nil, fmt.Errorf(
			"cannot parse link type %q to %q",
			link.Type,
			we.ProtoReflect().Descriptor().FullName(),
		)
	}

	if link.URL.Scheme != urlSchemeTemporalKey {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent")
	}

	matches := urlPathRE.FindStringSubmatch(link.URL.EscapedPath())
	if len(matches) != 4 {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent")
	}

	var err error
	we.Namespace, err = url.PathUnescape(matches[urlPathRE.SubexpIndex(urlPathNamespaceKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent")
	}

	we.WorkflowId, err = url.PathUnescape(matches[urlPathRE.SubexpIndex(urlPathWorkflowIDKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent")
	}

	we.RunId, err = url.PathUnescape(matches[urlPathRE.SubexpIndex(urlPathRunIDKey)])
	if err != nil {
		return nil, fmt.Errorf("failed to parse link to Link_WorkflowEvent")
	}

	switch link.URL.Query().Get(linkWorkflowEventReferenceTypeKey) {
	case string((&commonpb.Link_WorkflowEvent_EventReference{}).ProtoReflect().Descriptor().Name()):
		eventRef, err := convertURLQueryToLinkWorkflowEventEventReference(link.URL.Query())
		if err != nil {
			return nil, err
		}
		we.Reference = &commonpb.Link_WorkflowEvent_EventRef{
			EventRef: eventRef,
		}
	default:
		// TODO(rodrigozhou): do we want to return an error here?
	}

	return we, nil
}

func convertLinkWorkflowEventEventReferenceToURLQuery(eventRef *commonpb.Link_WorkflowEvent_EventReference) string {
	values := url.Values{
		linkWorkflowEventReferenceTypeKey: []string{string(eventRef.ProtoReflect().Descriptor().Name())},
		linkEventReferenceEventIDKey:      []string{strconv.FormatInt(eventRef.GetEventId(), 10)},
		linkEventReferenceEventTypeKey:    []string{eventRef.GetEventType().String()},
	}
	return values.Encode()
}

func convertURLQueryToLinkWorkflowEventEventReference(queryValues url.Values) (*commonpb.Link_WorkflowEvent_EventReference, error) {
	eventID, err := strconv.ParseInt(queryValues.Get(linkEventReferenceEventIDKey), 10, 64)
	if err != nil {
		return nil, err
	}
	eventType, err := enumspb.EventTypeFromString(queryValues.Get(linkEventReferenceEventTypeKey))
	if err != nil {
		return nil, err
	}
	return &commonpb.Link_WorkflowEvent_EventReference{
		EventId:   eventID,
		EventType: eventType,
	}, nil
}
