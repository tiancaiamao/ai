; Store operations test
* = $8000

start:
    lda #$10
    sta $FE
    sta $FF
    sta $FF+1
    sta $100
    jsr sub1
    rts

sub1:
    rts             ; Return
