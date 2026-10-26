`go generate ./internal/agentbin` builds the node agent here, for
linux/amd64 and linux/arm64, and the tailsuite binary embeds it. The
binaries are ignored by git. This file keeps the folder, so a plain
`go build` compiles without them.
