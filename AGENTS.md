# Core source boundaries

This repository owns Server, Web UI, contracts and Docker/Worker source. Keep the
Web source canonical; desktop clients consume a versioned UI artifact. Do not
introduce desktop source imports into core builds or tests. Keep user examples in
isolated acceptance tests, not production prompts or provider routing. All
assistant-authored model system prompts and built-in Skill instructions must be
in English. Preserve user content and third-party metadata.

Use synthetic data for tests. Source build success is not deployment proof.
The dedicated-host installer is `install.sh` -> `deploy/self-host/tofi_host.py`
(released by `.github/workflows/release.yml`); treat it as release-candidate
until clean Linux/KVM acceptance in docs/agent-plan/one-command-install.md passes,
and change it, its compose.yaml and the release workflow together. Never read or export credentials for a
source-tree review. Retain third-party notices and verify release contents.
