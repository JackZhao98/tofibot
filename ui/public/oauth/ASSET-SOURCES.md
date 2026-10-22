# OAuth provider artwork

- `provider-generic.svg`: authored neutral connector pictogram; no provider identity. Used for unknown or custom services.
- Real provider marks (Google G, Gmail, Calendar, Drive, Docs, Sheets, Slides, Notion) are served from `/brand/services/` (`ui/public/brand/services`); sources, licences and sha256 are in `docs/integrations/icons/manifest.json` and `NOTICE.md`. The completion page only renders logos on the allow-list in `internal/app/oauth_completion.go`.
- Tofi artwork reuses the incumbent `/brand/tofi-logo-color.svg`.

All artwork is served locally. The connection page makes no provider image or tracking requests.
