# memory-calc-web

A small local CLI that starts a web UI for exploring the Paketo JVM memory
calculator. Enter a container memory limit and the rest of the inputs, and see
exactly which memory regions the calculator selects — plus a stacked
visualization of how the JVM memory is divided.

Everything is supplied through the UI. The app never probes cgroups,
`/proc/meminfo`, or environment variables, so it can simulate a container that
is nothing like your local environment. The one optional local read is class
counting: when you choose a folder or JAR, the server counts classes in that
path only.

The calculation itself is not reimplemented. The server imports
`github.com/paketo-buildpacks/jvm-vendors/calc` directly, so the numbers match
the buildpack's calculator.

## Requirements

- Go 1.27 or newer

The calculator and class counting come from the released
[`github.com/paketo-buildpacks/jvm-vendors`](https://github.com/paketo-buildpacks/jvm-vendors)
module, currently `v0.3.10`. No local checkout is needed.

To pick up a newer release:

```bash
go get github.com/paketo-buildpacks/jvm-vendors@latest
```

## Run

### Without a clone

Install the binary:

```bash
go install github.com/dmikusa/memory-calc-web@latest
memory-calc-web
```

Or run it directly:

```bash
go run github.com/dmikusa/memory-calc-web@latest
```

Both fetch the latest version from the Go module proxy and start the server on
`http://127.0.0.1:8080`. `go install` places the binary in `$(go env GOPATH)/bin`
(or `$GOBIN` if set), so make sure that directory is on your `PATH`.

### From a clone

```bash
git clone https://github.com/dmikusa/memory-calc-web.git
cd memory-calc-web
go run .
```

### Options

Flags work with either approach:

```bash
memory-calc-web -listen 127.0.0.1:9000   # different address/port
memory-calc-web -no-open                 # don't launch a browser

go run github.com/dmikusa/memory-calc-web@latest -listen 127.0.0.1:9000
go run . -no-open
```

Build a binary from a clone:

```bash
go build -o memory-calc-web .
./memory-calc-web
```

## Class counts

The **Class counts** section has two modes:

- **Count from app** (default): choose a folder or JAR, and its class count fills
  the **App classes** component. That value is combined with the JVM, agent, and
  adjustment assumptions under **Class count assumptions**, then multiplied by
  `0.35` to produce the loaded class count used for metaspace sizing.
- **Manual entry**: choose **Total** to supply the final loaded class count
  directly (used as-is, with no `0.35` factor), or **Breakdown by type** to enter
  the JVM, app, agent, and adjustment values yourself.

Counting a folder or JAR returns a single total for that app. It does not break
the app down by class type; the other components come from the assumptions.

Use **Choose folder…**, **Choose JAR…**, or **Count** (for a typed path). The
path is sent to `/api/count-classes`, which calls `count.Classes` for directories
and `count.JarClassesFrom` for JARs — the same code the buildpack uses.

Native pickers use `osascript` on macOS, `zenity` or `kdialog` on Linux, and
PowerShell on Windows. If no dialog is available, paste a path and press Count.

## Inputs and their buildpack equivalents

| UI input | Buildpack source |
| --- | --- |
| Memory limit | cgroup limit / `/proc/meminfo` (simulated; blank = 1G default) |
| Head room | `BPL_JVM_HEAD_ROOM` |
| Thread count | `BPL_JVM_THREAD_COUNT` (blank = 250 default) |
| Loaded class count | `BPL_JVM_LOADED_CLASS_COUNT` |
| Class count derivation | `BPI_JVM_CLASS_COUNT` + app/agent class counts + `BPL_JVM_CLASS_ADJUSTMENT` |
| Low memory profile | `BPL_LOW_MEMORY_PROFILE_DISABLED=false` |
| `JAVA_TOOL_OPTIONS` | `JAVA_TOOL_OPTIONS` |
| JVM version | `BPI_JVM_VERSION` |

Application and agent class counts normally come from scanning the app
directory and JARs at build time. Since this is a simulation, those counts are
typed in instead. When "Loaded class count" is left blank, the UI applies the
same formula as the buildpack:

```
adjustment × (jvm + app + agent + static) × 0.35
```

## Behavior worth knowing

- An empty memory limit falls back to 1G, and limits above 64T are capped, matching
  the buildpack.
- When memory is below 1G and the low-memory profile is off, the UI warns and
  recommends enabling it.
- The low-memory profile is opt-in here, mirroring current production behavior:
  it only applies when the disabled flag is explicitly set to false.
- Options already set in `JAVA_TOOL_OPTIONS` (for example `-Xmx`) are treated as
  user-configured. They are preserved, and the calculator does not emit a
  duplicate value for them.
- Generated options follow the buildpack's ordering: direct memory, heap,
  metaspace, code cache, stack, then any Java 25+ compact object header flag.
