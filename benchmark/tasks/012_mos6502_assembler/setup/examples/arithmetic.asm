; Arithmetic operations test
* = $8000

start:
    lda #$10
    clc
    adc #$05        ; A = $15
    sec
    sbc #$03        ; A = $12
    inc $00         ; Increment memory
    dec $00         ; Decrement memory
    inx             ; Increment X
    dey             ; Decrement Y
    rts
