# Homebrew

The intended Homebrew package name is `whop2p`:

```sh
brew install techmore/tap/whop2p
whop2p
```

The package should install the short `whop2p` executable and keep
`who-pirates-the-pirates` as a discoverable alias. The display name remains
**Who Pirates The Pirates**.

The formula is intentionally not added to the main repository until a versioned
release tarball and its SHA-256 are published. A tap formula must pin a
reproducible source archive; do not publish a formula pointing at a moving
`main` branch.

Release checklist:

1. Tag a version, for example `v0.1.0`.
2. Publish the source archive for the tag.
3. Compute and record its SHA-256.
4. Add `whop2p.rb` to the `techmore/homebrew-tap` repository.
5. Run `brew audit --strict --online` against the tap.
6. Verify `brew install techmore/tap/whop2p` on a clean Apple Silicon Mac.

The formula should install the binary, documentation, and an optional service
definition, but it should never create or overwrite a user's catalog database
during `brew install`.
