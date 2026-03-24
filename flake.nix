{
  description = "Local CI tool — run commands on Nix platforms with GitHub status reporting";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs = inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      perSystem = { pkgs, ... }: {
        packages.default = pkgs.buildGoModule {
          pname = "giton";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
          meta.description = "Local CI tool — run commands on Nix platforms with GitHub status reporting";
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            wrapProgram $out/bin/giton \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.git pkgs.gh pkgs.nix pkgs.openssh pkgs.process-compose ]}
          '';
        };
      };
    };
}
