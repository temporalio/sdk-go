# WIT Overrides

Normally, `system-nexus-gen` generates code using `.wit` files it finds in the `go.temporal.io/api` dependency. But sometimes those files are out of date, or you'd just like to see what will happen when those files change. That's what this directory is for.

Any `.wit` files in this directory (and its subdirectories) will take precedence over files with the same relative path that come from `go.temporal.io/api`.