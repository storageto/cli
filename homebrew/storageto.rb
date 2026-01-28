# Homebrew formula for storageto
# To use: brew install storageto/tap/storageto
#
# Copy this file to the homebrew-tap repo:
# storageto/homebrew-tap/Formula/storageto.rb

class Storageto < Formula
  desc "CLI tool for storage.to - simple file sharing"
  homepage "https://github.com/storageto/cli"
  version "0.1.3"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/storageto/cli/releases/download/v#{version}/storageto-darwin-arm64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_AFTER_RELEASE"
    end
    on_intel do
      url "https://github.com/storageto/cli/releases/download/v#{version}/storageto-darwin-amd64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_AFTER_RELEASE"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/storageto/cli/releases/download/v#{version}/storageto-linux-arm64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_AFTER_RELEASE"
    end
    on_intel do
      url "https://github.com/storageto/cli/releases/download/v#{version}/storageto-linux-amd64.tar.gz"
      sha256 "REPLACE_WITH_SHA256_AFTER_RELEASE"
    end
  end

  def install
    bin.install "storageto"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/storageto version")
  end
end
