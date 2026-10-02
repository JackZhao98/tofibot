# Source separation verification

This is a private source migration, not a production upgrade or open-source
release. Project licensing remains undecided. No production operations were
performed by the directory separation.

- Core: fresh locked Web dependency install and Web build pass; Go Server/Guest
  builds and all Go package tests pass with the delivered module namespace.
- Standalone desktop: fresh locked dependency install, reviewed Web artifact
  import, native helper compilation and default macOS packaging pass. No App
  installation or launch was performed. Desktop unit tests: 705 pass, five skip.
  Its independent diagnostic Go module tests and Linux guest cross-build pass.
  The actual client/core synthetic integration also passes: two pairings, three
  file actions, capability withdrawal, offline failure, credential revocation,
  stable identity after Server restart and no uncertain-job replay.
- Seven installer synthetic failure-path unit checks pass. The dedicated-host
  installer remains unfinished; no permission policy or fresh host was applied.
- Original design token/icon expectations needed by Web tests are isolated
  under ui/test-fixtures. Historical icon/foreground guard checks currently fail
  on existing decorative SVG and foreground fill-token usage. This record does
  not claim the entire Web test set passes.

Only allowlisted committed source was copied. Private history, operational
acceptance records, deployment identity packets, desktop source and runtime data
are excluded. Complete delivered file hashes are in EXPORT-MANIFEST.json. Pattern
scan hits for SSH private-key markers were reviewed: a format detector and an
explicit invalid synthetic fixture, without a key payload. This limited scan
does not establish exhaustive security or legal review.
