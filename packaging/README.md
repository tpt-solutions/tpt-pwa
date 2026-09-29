# Packaging manifests

Per-release manifests for the native binaries (built by
`.github/workflows/release-binaries.yml` with SLSA provenance attestations
and per-target `SHA256SUMS-<target>.txt` files). The `PLACEHOLDER-SET-PER-RELEASE`
hash fields are filled from the checksums of the release being packaged —
never ship a manifest with a placeholder hash; the whole point is
verifiable installs.

| Directory | Target | Status |
| --- | --- | --- |
| `scoop/` | Windows (scoop bucket) | manifest ready — publish by mirroring into a `scoop-bucket` repo |
| `homebrew/` | macOS + Linux (Homebrew tap) | formula ready — publish by mirroring into a `homebrew-tap` repo |
| `winget/` | Windows (winget) | manifest ready — submit via a PR to `microsoft/winget-pkgs` under `TPT.Solutions.tpt-cortex` |

Per-release update flow (manual until automated):

1. Tag `vX.Y.Z`; wait for the `Release Binaries` workflow.
2. Fill the hash fields here from `SHA256SUMS-<target>.txt` of that release.
3. Mirror to the bucket/tap repos (or open the winget-pkgs PR).
