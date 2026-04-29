# Changelog

## 1.0.0 (2026-04-29)


### Features

* add coverage reporting and split lint/test workflows ([1b87844](https://github.com/mcriley821/jsonrpc2/commit/1b87844d8a517621ee6ea7c6c18d19820cf87f1e))
* add PR coverage comment with per-file diff ([3addbfb](https://github.com/mcriley821/jsonrpc2/commit/3addbfb43c689a25955957d1bbe3d45af515cacd))
* Batch calling ([#36](https://github.com/mcriley821/jsonrpc2/issues/36)) ([997307f](https://github.com/mcriley821/jsonrpc2/commit/997307f1769d04abde57a3b1496ad4bd90db5617))
* batch handling only. wip tests ([4021268](https://github.com/mcriley821/jsonrpc2/commit/402126823cdc1d99b8ff03212cec58c984084ac4))
* **conn:** receive-side batch handling (closes [#9](https://github.com/mcriley821/jsonrpc2/issues/9)) ([242e365](https://github.com/mcriley821/jsonrpc2/commit/242e3657137c43086e00882ad67410c7b411d5f3))
* initial commit ([44e8d1c](https://github.com/mcriley821/jsonrpc2/commit/44e8d1c3e6c571763b0eb30c3a10ffea924dacec))


### Bug Fixes

* add omitzero tag to requestObj.ID so notifications omit id field ([c458454](https://github.com/mcriley821/jsonrpc2/commit/c458454077b4e18ad27e6328e87bbfdceee7403c)), closes [#1](https://github.com/mcriley821/jsonrpc2/issues/1)
* add omitzero to requestObj.ID so notifications omit id on wire ([db6810a](https://github.com/mcriley821/jsonrpc2/commit/db6810add0a234fe78c5605e4b684309d54fcadb))
* **conn:** eliminate TOCTOU race between closed check and inflight insert ([456932b](https://github.com/mcriley821/jsonrpc2/commit/456932b77949be50211d8d29bb466021fd4bd1ff))
* **conn:** eliminate TOCTOU race between closed check and inflight insert ([3a3676d](https://github.com/mcriley821/jsonrpc2/commit/3a3676d6445a8c89c395aeb9b83c9410a736717f)), closes [#13](https://github.com/mcriley821/jsonrpc2/issues/13)
* fix lint issues and format ([ff4751a](https://github.com/mcriley821/jsonrpc2/commit/ff4751ab47255e4e95473a131595d5115c5daec0))
* **lint:** exclude gosec G118 for intentional cancel storage pattern ([48d0c57](https://github.com/mcriley821/jsonrpc2/commit/48d0c572caf50117f5d859cd630a777a423f826d))
* **lint:** remove unused nolint:gosec directive ([7745330](https://github.com/mcriley821/jsonrpc2/commit/7745330b52ad9ad8f7c5fdf181c3dc62c001288e))
* **lint:** resolve unparam, nolintlint, and wsl_v5 violations ([333e42e](https://github.com/mcriley821/jsonrpc2/commit/333e42efd2745d4882a6d730ce096799f9cc4616))
* **lint:** restore gosec nolint, allow unused directives in nolintlint ([e0863c7](https://github.com/mcriley821/jsonrpc2/commit/e0863c7972397d927a3e93225d1023086f28eea4))
* **lint:** shorten assert message in TestHandleNotification_RegularRequest_NoReply ([702ae40](https://github.com/mcriley821/jsonrpc2/commit/702ae402b6fac760d41c79e66ae57809f3542913))
* **lint:** use gosec excludes for G118 instead of invalid v2 key ([42cefcd](https://github.com/mcriley821/jsonrpc2/commit/42cefcda32d9cb4266e7902f2b77949eb0586242))
* robust batch detection and correct empty-batch test ([0bda0b4](https://github.com/mcriley821/jsonrpc2/commit/0bda0b4111d2e1c9b725d584dc88a47f87585e0d))
* update Codecov badge URL with correct token and path ([3082a3a](https://github.com/mcriley821/jsonrpc2/commit/3082a3a758a06e870a0ddf67c0f314c89f7a130c))
