# Project

Single bash script (`giton`) packaged as a Nix flake via `writeShellApplication`. Runtime deps: git, gh, nix.

# Dev

- Build: `nix build`
- Test: `nix run . -- --system $(nix eval --raw --impure --expr builtins.currentSystem) --name test -- echo hello`

# Key details

- GitHub status context format: `giton/<system>/<name>`
- Phase 2 (remote SSH execution) is not yet implemented
- `flake.nix` uses flake-parts
