# Homebrew formula for tpt-cortex (daemon + engine). Install from a tap:
#   brew tap tpt-solutions/tap https://github.com/tpt-solutions/homebrew-tap
#   brew install tpt-cortex
# Live in the tap repo; this copy is the source of truth. The class name and
# sha256 are bumped per release (sha256 from the per-target SHA256SUMS files).
# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
class TptCortex < Formula
  desc "Local native companion for the tpt-pwa framework (daemon + .ctx engine)"
  homepage "https://github.com/tpt-solutions/tpt-pwa"
  version "0.1.0"
  license "MIT OR Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/tpt-solutions/tpt-pwa/releases/download/v#{version}/tpt-cortex-macos-arm64.tar.gz"
      sha256 "PLACEHOLDER-SET-PER-RELEASE-FROM-SHA256SUMS-macos-arm64.txt"
    else
      url "https://github.com/tpt-solutions/tpt-pwa/releases/download/v#{version}/tpt-cortex-macos-amd64.tar.gz"
      sha256 "PLACEHOLDER-SET-PER-RELEASE-FROM-SHA256SUMS-macos-amd64.txt"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/tpt-solutions/tpt-pwa/releases/download/v#{version}/tpt-cortex-linux-arm64.tar.gz"
      sha256 "PLACEHOLDER-SET-PER-RELEASE-FROM-SHA256SUMS-linux-arm64.txt"
    else
      url "https://github.com/tpt-solutions/tpt-pwa/releases/download/v#{version}/tpt-cortex-linux-amd64.tar.gz"
      sha256 "PLACEHOLDER-SET-PER-RELEASE-FROM-SHA256SUMS-linux-amd64.txt"
    end
  end

  def install
    bin.install "cortex-daemon"
    bin.install "cortex-engine"
  end

  def caveats
    <<~EOS
      Run the pre-flight check first:
        cortex-daemon doctor
      The daemon binds the loopback by default (127.0.0.1:9911) and is
      flag-driven — no config files. In shared environments start it with
      -auth-token <random>.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/cortex-daemon doctor --json")
  end
end
