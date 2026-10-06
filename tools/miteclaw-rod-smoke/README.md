# MiteClaw Moli CDP smoke

Run the same overlay geometry and coordinate-click probe used by MiteClaw's
browser-backend gate against a built Moli binary:

```powershell
go run -mod=readonly . C:\absolute\path\to\moli.exe
```

The probe checks that the existing `--layout` policy leaves the dynamic
overlay geometry stale, while `--layout --fresh-geometry` reports the overlay
and routes a coordinate click to it. Both modes must block navigation to a
private-network fixture. It prints one result line per behavior and exits
non-zero on any mismatch.
