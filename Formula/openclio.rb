class Openclio < Formula
  desc "Local-first personal AI agent — single binary, no telemetry"
  homepage "https://github.com/openclio/openclio"
  version "0.1.0"

  on_macos do
    on_arm do
      url "https://github.com/openclio/openclio/releases/download/v#{version}/openclio-darwin-arm64"
      sha256 "PLACEHOLDER_ARM64_SHA256"
    end
    on_intel do
      url "https://github.com/openclio/openclio/releases/download/v#{version}/openclio-darwin-amd64"
      sha256 "PLACEHOLDER_AMD64_SHA256"
    end
  end

  def install
    if Hardware::CPU.arm?
      bin.install "openclio-darwin-arm64" => "openclio"
    else
      bin.install "openclio-darwin-amd64" => "openclio"
    end
  end

  test do
    system "#{bin}/openclio", "version"
  end
end
