# giton

Local CI tool. Runs commands on specific Nix platforms and posts GitHub commit statuses.

## Usage

```
giton --system <nix-system> --name <check-name> -- <command...>
```

## Dev

- Build: `nix build`
- Run: `nix run . -- --system x86_64-linux --name test -- echo hello`

## Architecture

Single bash script packaged via `writeShellApplication`. Runtime deps: git, gh, nix.

GitHub status context format: `giton/<system>/<name>`
