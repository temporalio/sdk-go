package internal

import (
	"fmt"

	"go.temporal.io/api/compute/v1"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/converter"
)

func computeConfigFromProto(
	dc converter.DataConverter,
	msg *compute.ComputeConfig,
) (*ComputeConfig, error) {
	if msg == nil {
		return nil, nil
	}

	res := &ComputeConfig{}
	groups := make(map[string]*ComputeConfigScalingGroup, len(msg.ScalingGroups))
	for groupName, group := range msg.ScalingGroups {
		g, err := computeConfigScalingGroupFromProto(dc, group)
		if err != nil {
			return nil, err
		}
		groups[groupName] = g
	}
	res.ScalingGroups = groups
	return res, nil
}

func validateComputeConfig(cfg *ComputeConfig) error {
	if cfg == nil {
		return nil
	}
	for groupName, group := range cfg.ScalingGroups {
		err := validateComputeConfigScalingGroup(groupName, group)
		if err != nil {
			return err
		}
	}
	return nil
}

func computeConfigToProto(
	dc converter.DataConverter,
	cc *ComputeConfig,
) (*compute.ComputeConfig, error) {
	if cc == nil {
		return nil, nil
	}

	groups := make(
		map[string]*compute.ComputeConfigScalingGroup,
		len(cc.ScalingGroups),
	)
	for groupName, group := range cc.ScalingGroups {
		g, err := computeConfigScalingGroupToProto(dc, group)
		if err != nil {
			return nil, err
		}
		groups[groupName] = g
	}
	return &compute.ComputeConfig{
		ScalingGroups: groups,
	}, nil
}

func computeConfigScalingGroupFromProto(
	dc converter.DataConverter,
	msg *compute.ComputeConfigScalingGroup,
) (*ComputeConfigScalingGroup, error) {
	if msg == nil {
		return nil, nil
	}

	res := &ComputeConfigScalingGroup{}
	msgTQTs := msg.GetTaskQueueTypes()
	tqts := make([]TaskQueueType, len(msgTQTs))
	for x, msgTQT := range msgTQTs {
		tqts[x] = taskQueueTypeFromProto(msgTQT)
	}
	res.TaskQueueTypes = tqts
	p, err := computeProviderFromProto(dc, msg.GetProvider())
	if err != nil {
		return nil, err
	}
	res.Provider = p
	s, err := computeScalerFromProto(dc, msg.GetScaler())
	if err != nil {
		return nil, err
	}
	res.Scaler = s
	return res, nil
}

func validateComputeConfigScalingGroup(
	groupName string,
	cfg *ComputeConfigScalingGroup,
) error {
	if cfg == nil {
		return nil
	}
	p := cfg.Provider
	if p != nil {
		if p.Type == "" {
			return fmt.Errorf(
				"compute config scaling group %s missing provider type",
				groupName,
			)
		}
		if p.Details == nil {
			return fmt.Errorf(
				"compute config scaling group %s missing provider details",
				groupName,
			)
		}
	}
	s := cfg.Scaler
	if s != nil {
		if s.Type == "" {
			return fmt.Errorf(
				"compute config scaling group %s missing scaler type",
				groupName,
			)
		}
		if s.Details == nil {
			return fmt.Errorf(
				"compute config scaling group %s missing scaler details",
				groupName,
			)
		}
	}
	return nil
}

func computeConfigScalingGroupToProto(
	dc converter.DataConverter,
	group *ComputeConfigScalingGroup,
) (*compute.ComputeConfigScalingGroup, error) {
	if group == nil {
		return nil, nil
	}

	tqts := group.TaskQueueTypes
	msgTQTs := make([]enums.TaskQueueType, len(tqts))
	for x, tqt := range tqts {
		msgTQTs[x] = taskQueueTypeToProto(tqt)
	}
	provider, err := computeProviderToProto(dc, group.Provider)
	if err != nil {
		return nil, err
	}
	scaler, err := computeScalerToProto(dc, group.Scaler)
	if err != nil {
		return nil, err
	}
	return &compute.ComputeConfigScalingGroup{
		TaskQueueTypes: msgTQTs,
		Provider:       provider,
		Scaler:         scaler,
	}, nil
}

func computeProviderToProto(
	dc converter.DataConverter,
	p *ComputeProvider,
) (*compute.ComputeProvider, error) {
	if p == nil {
		return nil, nil
	}
	res := &compute.ComputeProvider{
		Type: p.Type,
	}
	details := p.Details
	enc, err := dc.ToPayload(&details)
	if err != nil {
		return nil, err
	}
	res.Details = enc
	return res, nil
}

func computeScalerToProto(
	dc converter.DataConverter,
	s *ComputeScaler,
) (*compute.ComputeScaler, error) {
	if s == nil {
		return nil, nil
	}
	res := &compute.ComputeScaler{
		Type: s.Type,
	}
	details := s.Details
	enc, err := dc.ToPayload(&details)
	if err != nil {
		return nil, err
	}
	res.Details = enc
	return res, nil
}

func computeProviderFromProto(
	dc converter.DataConverter,
	msg *compute.ComputeProvider,
) (*ComputeProvider, error) {
	if msg == nil {
		return nil, nil
	}

	res := &ComputeProvider{
		Type:          msg.GetType(),
		NexusEndpoint: msg.GetNexusEndpoint(),
	}
	details := make(map[string]any)
	if details != nil {
		err := dc.FromPayload(msg.GetDetails(), details)
		if err != nil {
			return nil, err
		}
		res.Details = details
	}
	return res, nil
}

func computeScalerFromProto(
	dc converter.DataConverter,
	msg *compute.ComputeScaler,
) (*ComputeScaler, error) {
	if msg == nil {
		return nil, nil
	}

	res := &ComputeScaler{
		Type: msg.GetType(),
	}
	details := make(map[string]any)
	if details != nil {
		err := dc.FromPayload(msg.GetDetails(), details)
		if err != nil {
			return nil, err
		}
		res.Details = details
	}
	return res, nil
}
