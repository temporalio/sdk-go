This package is vendored based off of the [gopsutil](https://github.com/shirou/gopsutil)
package, where we've stripped everything except the CPU and mem measuring functionality.
We also only need to support Darwin, Linux, and Windows measurements, as those are 
the platforms the SDK itself supports. `LICENSE` has been included in this directory
to honor the BSD license of gopsutil.

When making changes to update with upstream, use the `scripts/compare_with_gopsutil.sh` 
to compare the results of the vendored package with using the library directly.
CI also runs this script to ensure there are no unexpected discrepancies.
