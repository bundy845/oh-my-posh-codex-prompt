# Oh My Posh Codex Prompt

A standalone `codex-prompt` helper for the official [Oh My Posh](https://github.com/JanDeDobbeleer/oh-my-posh)
release. Reads saved local Codex rollout files and prints a compact prompt fragment:

```text
cx gpt-example 95% ↻35m
cx 95% ↻35m
```

`95%` means **primary quota remaining**. The model appears only when a saved session matches the current project.
The reset countdown rounds down to whole minutes, uses `2h15m` for longer intervals, and shows `<1m` in the final
minute. Expired quota windows disappear until Codex saves a newer snapshot. No usable data means no output.

## Build and install

Requires Go 1.25 or later; there are no external dependencies.

```sh
go test ./...
mkdir -p "$HOME/.local/bin"
go build -trimpath -o "$HOME/.local/bin/codex-prompt" .
```

Rebuild only when this helper changes. Update the official Oh My Posh installation normally; this program does
not require a fork checkout, daily builds, a running service, credentials, or network access.

## Oh My Posh configuration

Add a standard text segment to your local theme. Keep your theme's colors and style as desired:

```json
{
  "type": "text",
  "style": "plain",
  "foreground": "#74AA9C",
  "template": "{{ with cmd (printf \"%s/.local/bin/codex-prompt\" .Env.HOME) }} {{ . }} {{ end }}"
}
```

The absolute executable path avoids PATH ordering issues during shell initialization. Do not cache the text
segment: the helper caches discovery and parsing but calculates the countdown at each prompt render.

## Behavior and options

- `--codex-home DIR`: override `$CODEX_HOME`, which otherwise falls back to `~/.codex`. A leading `~/` is expanded.
- `--cwd DIR`: select a project directory instead of the process's working directory.
- `--cache-dir DIR`: override the cache location. Defaults to `oh-my-posh-codex-prompt/v1` inside the operating
  system's user-cache directory (`~/Library/Caches` on macOS).
- `--help`: show usage. Invalid arguments produce diagnostics and exit status 2.

Within Git, session selection includes the closest repository/worktree root and its descendants. Outside Git,
the session directory must match exactly. The latest matching session update wins. Primary account quota is
selected independently from the latest saved quota snapshot across all sessions in the chosen Codex home.
Secondary quota is parsed to preserve snapshot selection and expiry behavior but is not displayed.

Discovery results are cached for 30 seconds. A reported reset time bypasses that cache and rereads the affected
rollout even if its file size and modification time are unchanged. Otherwise, unchanged files reuse parsed
metadata. Cache files are private, written atomically, and contain only timestamps, file metadata, session IDs,
directories, model identifiers, and quota values. Transcript text is neither stored in the cache nor logged.
Missing or malformed cache files recover automatically; unwritable caches fall back to uncached reads.

## Limitations

This reader targets the internal JSONL rollout format observed with Codex 0.166.0. Codex may change that format;
sessions stored only in paginated storage are unsupported. Values are saved snapshots, not live account quotas,
ChatGPT usage, or API billing. Switching accounts can leave snapshots associated with a previous account.
Concurrent sessions within the same project cannot be tied reliably to a particular terminal.

## License and origins

Released under the [MIT License](LICENSE). This standalone helper implements the Codex segment. The license includes
bundy845's copyright notice and retains the original Oh My Posh copyright notice. This is an independent
companion project, not an official Oh My Posh release.
