Marked `workflow.SignalWithStart` as experimental.
`client.Options.SdkName` and `client.Options.SdkVersion` now also apply to the SDK metadata of workflow task completions, not only to worker heartbeats.
Workers no longer advertise a worker control task queue unless worker heartbeats and commands are supported and the shared namespace command poller is running.
Workers let the server complete outstanding worker-command polls on namespaces that support graceful poll shutdown.