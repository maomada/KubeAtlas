# PR #22 security revalidation

[Security run 44550600016](https://github.com/maomada/KubeAtlas/actions/runs/44550600016) scanned all 1,994 production files with Semgrep `1.180.0` and the pinned rules lock. Its merge source `7fc3544c1dcea21106c692b81c240915426e0c0f` has the same Git tree as reviewed head `54257b42f598398f6f2f5674879e87dec9f6816e`.

| Evidence | SHA-256 |
| --- | --- |
| Production source | `c84110c57e66f9b0f82c2b35888f793364e46ddea883be03f7eb831d53bfc8a8` |
| Raw findings | `cbf3c311ff172dab6fb4e2dd3364c1a1a664c92bb664c787271f905a518423d5` |
| Rules lock | `296024fcf2db65e68a0d7410d2723611ddf1d4aae917a0cd82b5bc57ebcd6f5d` |

The report contains 112 findings and zero scanner errors. Reviewers matched all 112 source spans byte for byte to the previous review. SAST-079 moved from line 461 to line 464 after the application annotation display change. Its matched version parser and surrounding function are identical. The only other modified finding file changes a checkbox color token.

The three HTTP-IP findings retain the original maintainer decision for the explicitly configured intranet authority. Their cookie writes and transport guards are byte-identical to the previously reviewed source. The refreshed policy matches 109 technical dispositions and the three existing accepted risks with zero unresolved findings.

The subsequent cluster-checks documentation and Linux packaging changes have the same production source digest. The final PR checks rescan that source and validate the new package matrix.
