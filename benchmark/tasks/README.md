# Benchmark Task Manifests

## Available Manifests

### `fast-manifest.json`
Tasks that complete in under 10 minutes (64 tasks).

**Excluded slow tasks (>10min based on historical data):**
- tbench/dna-assembly (~29min)
- tbench/distribution-search (~24min)
- tbench/count-dataset-tokens (~21min)
- tbench/dna-insert (~16min)
- 010_basic_interpreter (~15min)
- tbench/adaptive-rejection-sampler (~15min)
- tbench/bn-fit-modify (~15min)
- tbench/compile-compcert (~14min)
- 012_mos6502_assembler (~13min)
- tbench/caffe-cifar-10 (~10min)

### Default Manifest
Without specifying a manifest, all 74 available tasks will run.

## Usage

```bash
# Run fast tasks only (64 tasks, estimated ~2 hours)
make bench-run MANIFEST=tasks/fast-manifest.json

# Run all tasks (74 tasks, estimated ~4 hours)
make bench-run

# List tasks in a specific manifest
make bench-list MANIFEST=tasks/fast-manifest.json

# Resume from previous run with specific manifest
make bench-resume MANIFEST=tasks/fast-manifest.json
```

## Adding New Manifests

Create a new JSON file in this directory with the following format:

```json
{
  "version": "1.0",
  "frozen_at": "YYYY-MM-DDTHH:MM:SS+08:00",
  "description": "Description of this task subset",
  "tasks": [
    "task_id_1",
    "task_id_2",
    ...
  ]
}
```
