# Workers drive devices with mobilecli from the shell, not through MCP

Workers call `mobilecli` (mobile-next) directly instead of the mobile-mcp
server. mobile-mcp is a wrapper around mobilecli, so the engine (accessibility
tree, uniform iOS/Android input) is the same; dropping the MCP layer removes
33 tool schemas from every worker's context, works in harnesses without MCP
support, and avoids mobile-mcp's default-on telemetry. mobilecli is not in
Homebrew, so `peuf0u/homebrew-tap` carries a `mobilecli` formula that
downloads mobile-next's official release zip (we never host or rebuild the
binary; FSL-1.1-ALv2 permits this for non-competing use) at a pinned, tested
version, and the simsquad formula depends on it, so one `brew install`
brings both and Node.js is not required. Costs: version bumps are a manual
tap edit; the iOS helper app is downloaded by mobilecli on first use, so the
first iOS run needs network access; and on Android mobilecli holds the
device's only UiAutomation connection, so no other UI-automation tool may
run alongside it.
