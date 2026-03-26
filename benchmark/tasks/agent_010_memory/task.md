# Task: Fix Multi-Stage Process (Long Horizon Memory)

## Description
A data processing pipeline has a bug. The pipeline has 5 stages.

## ⚠️ Long Horizon Memory Test

Information found in **Stage 1** will be needed in **Stage 5**.

Between stage 1 and stage 5, there are 3 intermediate stages with lots of
irrelevant information. You must remember the key information from stage 1.

## Pipeline Stages
1. `stage1_load.py` - Loads data (IMPORTANT INFO HERE)
2. `stage2_validate.py` - Validates data
3. `stage3_transform.py` - Transforms data
4. `stage4_filter.py` - Filters data
5. `stage5_save.py` - Saves data (NEEDS INFO FROM STAGE 1)

## Bug Location
The bug is in stage 5, but to fix it you need information from stage 1.

## Hint
Read stage 1 carefully. Remember key details. They'll be needed later.