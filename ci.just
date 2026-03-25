[metadata("depends_on", "build")]
test:
    nix run .#test
build:
    nix build
