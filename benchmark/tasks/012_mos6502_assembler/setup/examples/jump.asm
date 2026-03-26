; Jump and subroutine test
* = $8000

start:
    jsr sub1        ; Call subroutine
    jmp done        ; Jump to end

sub1:
    lda #$AA
    rts             ; Return

done:
    rts
