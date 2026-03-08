; Simple load/store test
* = $8000

start:
    lda #$42        ; Load immediate
    sta $00         ; Store to zero page
    ldx #$10        ; Load X
    stx $01         ; Store X
    ldy #$20        ; Load Y
    sty $02         ; Store Y
    rts
