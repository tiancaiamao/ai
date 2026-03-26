; acme: assemble with e.g.
;   acme -f cbm -o chars.prg chars.asm
; Then LOAD"CHARS",8,1 and SYS 4096

* = $8000

SCREEN  = $0400          ; start of screen RAM
ROWS    = 16
COLS    = 16

; use some standard zero-page temp locations
ptr     = $fb            ; 2 bytes: screen pointer
ch      = $fd            ; current character

start:
        ; init screen pointer to $0400
        lda #<SCREEN
        sta ptr
        lda #>SCREEN
        sta ptr+1

        lda #$00         ; starting character code = 0
        sta ch

        ldx #ROWS        ; 16 rows

row_loop:
        ldy #0           ; column 0..15

col_loop:
        lda ch
        sta (ptr),y      ; write char to screen
        inc ch           ; next character code
        iny
        cpy #COLS        ; done 16 cols?
        bne col_loop

        ; move pointer down by one text row (40 chars)
        clc
        lda ptr
        adc #40
        sta ptr
        bcc no_carry
        inc ptr+1
no_carry:
        dex
        bne row_loop

        rts              ; back to BASIC (SYS 4096 to run)
