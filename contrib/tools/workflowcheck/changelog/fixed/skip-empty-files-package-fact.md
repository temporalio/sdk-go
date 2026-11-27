Skip exporting package facts for fileless packages (such as `unsafe`) in `workflowcheck` to prevent an index-out-of-range panic when `-debug=f` or `golangci-lint` is used.
