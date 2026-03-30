package internal

// ComputeConfig wraps configuration settings for a compute provider and
// scaling for a set of TaskQueue name+type tuples.
type ComputeConfig struct {
	// ScalingGroups contains the set of ComputeConfigScalingGroup objects
	// associated with the ComputeConfig. The key for the map is the ID of the
	// scaling group.
	ScalingGroups map[string]*ComputeConfigScalingGroup
}

// ComputeConfigScalingGroup defines a set of configuration settings for a
// compute provider and scaling for a set of TaskQueue types.
type ComputeConfigScalingGroup struct {
	// TaskQueueTypes is the set of task queue types this scaling group serves.
	TaskQueueTypes []TaskQueueType
	// Provider contains the optional compute provider configuration settings.
	Provider *ComputeProvider
	// Scaler contains the optional compute scaler configuration settings.
	Scaler *ComputeScaler
}

// ComputeProvider describes configuration settings for a compute provider.
type ComputeProvider struct {
	// Type of the compute provider. This string is implementation-specific and
	// can be used by implementations to understand how to interpret the
	// contents of the details field.
	Type string
	// Details contains an implementation-specific thing that describes the
	// compute provider configuration settings.
	Details map[string]any
	// NexusEndpoint points at the Nexus service, if the compute provider is a
	// Nexus service.
	NexusEndpoint string
}

// ComputeScaler describes configuration settings for a scaler of compute.
type ComputeScaler struct {
	// Type of the compute scaler. This string is implementation-specific and
	// can be used by implementations to understand how to interpret the
	// contents of the details field.
	Type string
	// Details contains an implementation-specific thing that describes the
	// compute scaler configuration settings.
	Details map[string]any
}
