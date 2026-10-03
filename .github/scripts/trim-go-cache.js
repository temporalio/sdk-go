const fs = require("fs");
const path = require("path");

const hourMs = 60 * 60 * 1000;
const maxStalenessHours = 2;

const dir = process.argv[2];
const startMs = Number(process.env.CACHE_JOB_START_MS);
if (!dir || !Number.isFinite(startMs) || !fs.existsSync(dir)) {
  throw new Error("GOCACHE path and CACHE_JOB_START_MS are required");
}

const cutoff = startMs - maxStalenessHours * hourMs;
let keptFiles = 0;
let keptBytes = 0;
let removedFiles = 0;
let removedBytes = 0;
// Go refreshes recently used cache mtimes, making old entries safe to drop.
const pending = [dir];

while (pending.length > 0) {
  const current = pending.pop();
  for (const entry of fs.readdirSync(current, {withFileTypes: true})) {
    const entryPath = path.join(current, entry.name);
    if (entry.isDirectory()) {
      pending.push(entryPath);
      continue;
    }
    if (!entry.isFile()) {
      continue;
    }

    const stat = fs.statSync(entryPath);
    if (stat.mtimeMs >= cutoff) {
      keptFiles++;
      keptBytes += stat.size;
      continue;
    }

    fs.unlinkSync(entryPath);
    removedFiles++;
    removedBytes += stat.size;
  }
}

console.log(
  `GO_CACHE_TRIM cutoff=${new Date(cutoff).toISOString()} ` +
  `kept_files=${keptFiles} kept_bytes=${keptBytes} ` +
  `removed_files=${removedFiles} removed_bytes=${removedBytes}`,
);
