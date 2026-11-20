# PR #28 security revalidation

[Security run 44779619937](https://github.com/maomada/KubeAtlas/actions/runs/44779619937) scanned 1,995 production files with Semgrep `1.180.0` and the existing rules lock. The PR merge source `bb444ce39f7c51761a2791469ba5cd94bc85eefa` has the same Git tree as reviewed head `84cbe0c26ee008431e4f7da26de72f5573c28ff7`.

| Evidence | SHA-256 |
| --- | --- |
| Production source | `43dd7cbbc36d4fc8a657fa49a84f2d16e30018db7daea88f1cf82e1c29893719` |
| Raw findings | `1884e3cc2741670f5a22abe0d86486946fc9f7578dcd3a8d4782e642e3feed57` |
| Rules lock | `296024fcf2db65e68a0d7410d2723611ddf1d4aae917a0cd82b5bc57ebcd6f5d` |

111 findings match the previous review in rule, location, and source file. SAST-116 moved from line 102 to line 113 in `internal/hubartifacts/prepare.go`. The matched Helm invocation is byte-identical. The executable stays under the trusted local operator's control, and the command still passes its arguments directly to Helm. The new Portal artifact commands validate or import catalogs without invoking Helm.

The [initial PR scan](https://github.com/maomada/KubeAtlas/actions/runs/44778342260) flagged the archive footer check because it could keep decompressing extra tar data up to the archive size limit. The reviewed source reads only one byte past the tar terminator, requires gzip EOF, and retains the archive size limit. Regression tests cover trailing decompressed bytes and a corrupt gzip footer.

The scan reported zero errors. All 109 technical dispositions and three scoped maintainer risk acceptances retain their existing approvals and conditions.
