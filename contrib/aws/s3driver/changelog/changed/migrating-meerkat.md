**Experimental**: Store and retrieve operations now respect `ExternalStorage.Concurrency`.
**Experimental**: Payloads stored with no target are now written under `v0/ns/{namespace}/d/sha256/{hash}`, using the namespace of the client or worker (`null` when it is unknown), instead of `v0/d/sha256/{hash}`. Object keys for payloads with a target are unchanged, and existing objects remain retrievable.
