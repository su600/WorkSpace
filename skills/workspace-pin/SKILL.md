---
name: workspace-pin
description: Pin or unpin files and directories in a WorkSpace Portal homepage. Use when the user asks to pin, unpin, or manage pinned Workspace items.
---

# WorkSpace Portal pin API

Use the authenticated pin API provided by WorkSpace Portal. Configure `WORKSPACE_PORTAL_URL` (for example `https://workspace.example.com`) and provide a dedicated Bearer token through your agent's secure secret/environment configuration as `WORKSPACE_PIN_API_TOKEN`. Never commit, print, paste into chat, or log the token. Do not send credentials over plain HTTP except to a loopback/local portal.

Paths must be relative to the portal's configured workspace directory. Resolve conversational references against recent work, verify the target exists inside the workspace, and ask if the target is ambiguous. Never use `..`, absolute paths, or symlinks that escape the workspace.

## Operations

- Pin a file/directory: `POST ${WORKSPACE_PORTAL_URL}/pin` with JSON `{"action":"pin","path":"relative/path"}`.
- Unpin: same endpoint with `{"action":"unpin","path":"relative/path"}`.
- List homepage pins: `GET ${WORKSPACE_PORTAL_URL}/api/pins`.
- Send `Authorization: Bearer $WORKSPACE_PIN_API_TOKEN`; JSON POSTs also require `Content-Type: application/json`.
- Check the HTTP status and response before confirming the change to the user. Do not disclose the token in the response.

Example (ensure shell tracing is disabled; use your approved secret manager to expose these environment variables):

```sh
set +x
: "${WORKSPACE_PORTAL_URL:?Set the portal URL}"
: "${WORKSPACE_PIN_API_TOKEN:?Set the pin API token securely}"
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer ${WORKSPACE_PIN_API_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"action":"pin","path":"relative/path"}' \
  "${WORKSPACE_PORTAL_URL%/}/pin"
```
