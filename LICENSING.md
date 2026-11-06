# Licensing

BOS API's original source code is licensed under the MIT License. See [LICENSE](LICENSE)
for the complete terms. Community capacity limits describe the shipped
application behavior and do not add restrictions to the MIT License.

The commercial product's activation mechanism is separate from this source-code
license. No commercial activation service is required to build the community edition.

## Third-party dependencies and assets

Third-party packages and bundled assets retain their own copyright and licenses.
Original dependency notices are included in [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt)
and [third_party/licenses](third_party/licenses). Dependency versions are pinned
by `backend/go.mod`, `backend/go.sum`, and `frontend/pnpm-lock.yaml`.

The MySQL Go driver is licensed under MPL-2.0. Its source is available from
[the upstream v1.7.0 tag](https://github.com/go-sql-driver/mysql/tree/v1.7.0).
Redis, MySQL, container base images, and system packages have their own terms;
the project license does not replace them.

Product marks used in integration documentation identify their respective
products and do not imply endorsement. Their provenance and accompanying notices
are recorded under `frontend/public/docs/brands`.

No GeoLite2 database is bundled. Operators who choose to use one must obtain the
database themselves and comply with its license, attribution, and update terms.
