class Morse < Formula
  desc "Send a notification to Telegram from the command line"
  homepage "https://github.com/leue21/morse"
  url "https://github.com/leue21/morse/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "5b3589ee6d96c86c04ec2ad085bb4769e8cceaccd4d34028a9969db8ea28ca12"
  license "MIT"
  head "https://github.com/leue21/morse.git", branch: "main"

  depends_on "go" => :build

  def install
    # A tarball has no tag to describe, so the version is stamped in from the
    # formula rather than from git.
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags)
    pkgshare.install "config.yaml.example"
  end

  def caveats
    <<~EOS
      Credentials go in ~/.config/morse/config.yaml, or in MORSE_BOT_TOKEN and
      MORSE_CHAT_ID. To start from the example:

        mkdir -p ~/.config/morse
        cp #{opt_pkgshare}/config.yaml.example ~/.config/morse/config.yaml
        chmod 600 ~/.config/morse/config.yaml

      Then check it with: morse capabilities
    EOS
  end

  test do
    assert_match "send", shell_output("#{bin}/morse help")
    assert_match version.to_s, shell_output("#{bin}/morse version")
  end
end
