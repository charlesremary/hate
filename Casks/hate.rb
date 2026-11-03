# Homebrew cask for hate. This repo is the tap:
#   brew tap charlesremary/hate https://github.com/charlesremary/hate
#   brew install --cask charlesremary/hate/hate
#
# `make release` signs, notarizes and zips the binary, then runs `make cask`,
# which rewrites version and sha256 below from AppVersion and the tarball of
# the notarized binary. `make publish` commits that tarball as
# releases/hate-darwin-arm64.tar.gz, tags v<version> and pushes over SSH; the
# url reads it at that tag, so every version keeps its own download.
cask "hate" do
  version "2.0.0"
  sha256 "43bcb58ef82961e1ed950874dc9c4d082bcd967964c0445ada6b577ad91f2709"

  url "https://raw.githubusercontent.com/charlesremary/hate/v#{version}/releases/hate-darwin-arm64.tar.gz",
      verified: "raw.githubusercontent.com/charlesremary/hate/"
  name "hate"
  desc "Git-backed ticketing for humans and agents, with a built-in MCP server"
  homepage "https://github.com/charlesremary/hate"

  livecheck do
    url "https://github.com/charlesremary/hate.git"
    strategy :git
  end

  depends_on arch: :arm64
  depends_on macos: :monterey

  binary "hate"

  caveats <<~EOS
    Start hate with:
      hate              # opens http://localhost:8000/
      hate -no-browser

    Use it from Claude Code (hate must be running):
      claude mcp add --transport http --scope user hate http://127.0.0.1:8000/mcp
  EOS
end
