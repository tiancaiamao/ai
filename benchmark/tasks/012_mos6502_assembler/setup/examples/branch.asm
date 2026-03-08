; Branch instructions test
* = $8000

start:
    lda #$00
    beq zero        ; Branch if zero
    lda #$FF
zero:
    bne notzero     ; Branch if not zero
    lda #$01
notzero:
    rts
