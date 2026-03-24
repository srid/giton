{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs = inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      perSystem = { pkgs, ... }:
        let
          giton = pkgs.buildGoModule {
            pname = "giton";
            version = "0.1.0";
            src = ./..;
            vendorHash = null;
          };
          testFiles = pkgs.runCommand "giton-test-files" { } ''
            mkdir -p $out
            cp ${./run.sh} $out/run.sh
            cp ${./test_single_step.sh} $out/test_single_step.sh
            cp ${./test_github_status.sh} $out/test_github_status.sh
            cp ${./test_sha_pinning.sh} $out/test_sha_pinning.sh
            cp ${./test_multi_step.sh} $out/test_multi_step.sh
          '';
        in
        {
          packages.default = pkgs.writeShellApplication {
            name = "giton-test";
            runtimeInputs = [ pkgs.git pkgs.nix pkgs.process-compose giton ];
            text = ''
              export GITON="giton"
              exec bash ${testFiles}/run.sh "$@"
            '';
          };
        };
    };
}
