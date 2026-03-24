{
  description = "Local CI tool — run commands on Nix platforms with GitHub status reporting";

  inputs = {
    nixpkgs.follows = "lean4-nix/nixpkgs";
    flake-parts.url = "github:hercules-ci/flake-parts";
    lean4-nix.url = "github:lenianiva/lean4-nix";
  };

  outputs = inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      perSystem = { system, pkgs, ... }:
        let
          pkgs' = import inputs.nixpkgs {
            inherit system;
            overlays = [ (inputs.lean4-nix.readToolchainFile ./lean-toolchain) ];
          };
          gitonUnwrapped = (pkgs'.lean.buildLeanPackage {
            name = "Main";
            roots = [ "Main" ];
            src = pkgs'.lib.cleanSource ./.;
          }).executable;
          giton = pkgs'.stdenv.mkDerivation {
            pname = "giton";
            version = "0.1.0";
            dontUnpack = true;
            nativeBuildInputs = [ pkgs'.makeWrapper ];
            installPhase = ''
              mkdir -p $out/bin
              cp ${gitonUnwrapped}/bin/* $out/bin/giton
              chmod +x $out/bin/giton
              wrapProgram $out/bin/giton \
                --prefix PATH : ${pkgs'.lib.makeBinPath [ pkgs'.git pkgs'.gh pkgs'.nix pkgs'.openssh pkgs'.process-compose pkgs'.coreutils pkgs'.hostname ]}
            '';
            meta.description = "Local CI tool — run commands on Nix platforms with GitHub status reporting";
          };
          testFiles = pkgs'.runCommand "giton-test-files" { } ''
            mkdir -p $out
            cp ${./test/run.sh} $out/run.sh
            cp ${./test/test_single_step.sh} $out/test_single_step.sh
            cp ${./test/test_github_status.sh} $out/test_github_status.sh
            cp ${./test/test_sha_pinning.sh} $out/test_sha_pinning.sh
            cp ${./test/test_multi_step.sh} $out/test_multi_step.sh
          '';
        in
        {
          _module.args.pkgs = pkgs';

          packages.default = giton;

          # Test runner (uses unwrapped binary so mock gh takes precedence in PATH)
          packages.test = pkgs'.writeShellApplication {
            name = "giton-test";
            runtimeInputs = [ pkgs'.git pkgs'.nix pkgs'.process-compose ];
            text = ''
              export GITON="${gitonUnwrapped}/bin/main"
              exec bash ${testFiles}/run.sh "$@"
            '';
          };

          devShells.default = pkgs'.mkShell {
            packages = with pkgs'.lean; [ lean-all ];
          };
        };
    };
}
