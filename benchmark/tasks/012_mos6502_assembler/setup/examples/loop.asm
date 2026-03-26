; Simple loop test
* = $8000

start:
    ldx #$05        ; Counter = 5
loop:
    dex             ; Decrement
    bne loop        ; Loop until zero
    rts
