# Modelserver OAuth Project Context TODO

Status: postponed because this repository cannot change modelserver.

Current constraint:
- `GET /api/v1/projects` requires the modelserver dashboard JWT.
- The desktop installer receives a Hydra OAuth access token.
- If the OAuth access token is a JWT and contains `project_id`, the installer can read it locally.
- If the OAuth access token is opaque, the installer cannot resolve `Modelserver.ProjectID` without a modelserver endpoint that accepts the OAuth token.

Current installer behavior:
- Modelserver login does not call `GET /api/v1/projects` and does not block on project lookup.
- `Modelserver.ProjectID` is written only when the OAuth token itself contains `project_id`; otherwise it remains empty.
- Tray quota display uses `GET /v1/usage` with the OAuth token, so it can work while project id/name lookup is postponed.

TODO:
- Add a modelserver OAuth-token endpoint for current project context, or include `project_id` and `project_name` in an existing OAuth-token endpoint such as `GET /v1/usage`.
- After modelserver supports that, update the installer to use the endpoint as a fallback when local token claims do not contain `project_id`.
- Then use the resolved `project_id` for subscription links and quota/project display in the tray console.
