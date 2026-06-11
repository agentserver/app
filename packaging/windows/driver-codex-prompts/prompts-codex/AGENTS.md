# Agentserver Driver Workspace

- Use the `multiagent` skill when the user wants to inspect or use workspace resources, agents, or remote execution.
- Use the registered `mcp_servers.driver` MCP server as the source of truth for workspace agents, resources, and driver tools.
- Discover agents and resources before acting. Filter agents by `role == "slave"` and choose shell helpers from each target's `platform` and `command_interfaces`.
- For complex planning, debugging, implementation, or review tasks, use the installed Superpower skills. Start with `using-superpowers` when available.
