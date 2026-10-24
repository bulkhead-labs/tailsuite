# Vendored scripts

Copied byte for byte from cdnjs. The SRI hashes in `docs/index.html` must
match these files ([decision 0012](../../decisions/0012-status-page-hosting.md)).

| File | Library | Licence | sha384 | Checked |
|---|---|---|---|---|
| `marked-12.0.2.min.js` | [marked](https://github.com/markedjs/marked) 12.0.2 | MIT | `/TQbtLCAerC3jgaim+N78RZSDYV7ryeoBCVqTuzRrFec2akfBkHS7ACQ3PQhvMVi` | 2026-10-24 |
| `purify-3.4.16.min.js` | [DOMPurify](https://github.com/cure53/DOMPurify) 3.4.16 | Apache-2.0 or MPL-2.0 | `a7SzOxErzJ3ZpQz0zJ32d67dSitNzPcbfybc/ykU9KJhMgZkwqfSxlhhdJRS+XGL` | 2026-10-24 |

**To update:**

1. Download the new file from cdnjs. The release must be at least two
   weeks old.
2. Rename it with its version.
3. Compute the SRI:

   ```
   openssl dgst -sha384 -binary <file> | openssl base64 -A
   ```

4. Update `docs/index.html`, this table and decision 0012.
