# Project

Lean 4 CLI tool (`Main.lean`) packaged as a Nix flake via lean4-nix's `buildLeanPackage`. Runtime deps: git, gh, nix, openssh, process-compose.

# Dev

- Build: `nix build`
- Test: `nix run .#test`
- Dev shell: `nix develop` (provides lean4 toolchain)

# Key details

- GitHub status context format: `giton/<name>/<system>`
- Two modes: single-step (`-- <cmd>`) and multi-step (`-f config.json` via process-compose)
- `--sha` flag pins commit and skips clean-tree check (used for self-invocation in multi-step mode)
- `flake.nix` uses flake-parts with lean4-nix overlay
- `lean-toolchain` pins the Lean 4 version
