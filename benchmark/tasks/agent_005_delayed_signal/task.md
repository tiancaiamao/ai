# Task: Debug the Order Processing (Delayed Signal)

## Description
The order processing system has a bug where orders with discounts are being
calculated incorrectly.

## Symptoms
An order with items totaling $100 and a 10% discount should be $90,
but it's showing $95.

## Debug Information
The logs are stored in separate files:
- `logs/order_12345.log` - Main order log
- `logs/payment.log` - Payment processing log  
- `logs/discount.log` - Discount calculation log

Each log file only shows partial information. You must read ALL log files
to understand the complete flow and find where the bug is.

## Files
- `order_processor.py` - Main order processing logic
- `logs/` - Log files (read them to understand the bug)

## Hint
The bug is NOT in the order total calculation.
Check how discounts are applied by reading the logs.