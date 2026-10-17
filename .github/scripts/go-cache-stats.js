const fs = require("fs");
const path = require("path");

function size(dir) {
  let files = 0;
  let bytes = 0;

  if (!fs.existsSync(dir)) {
    return {files, bytes};
  }

  // Avoid platform-specific du output so CI logs are comparable.
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
      files++;
      bytes += stat.size;
    }
  }

  return {files, bytes};
}

for (const dir of process.argv.slice(2)) {
  const result = size(dir);
  console.log(`GO_CACHE_STATS path=${JSON.stringify(dir)} files=${result.files} bytes=${result.bytes}`);
}
