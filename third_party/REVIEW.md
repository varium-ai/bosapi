# Distribution review notes

The dependency inventory includes 207 production npm packages and 65 Go modules.
License and notice texts are collected under `third_party/licenses` and indexed
in `THIRD_PARTY_NOTICES.txt`. All indexed notice paths are present. BOS API's
original code is licensed separately under MIT.

## toggle-selection 1.0.6

The published npm archive declares MIT but omits a standalone license file.
Its CommonJS `index.js` was compared byte for byte with the MIT-licensed source
at upstream revision `888650b271ee4937e2baff6a43bee744635601e3` (version 2.1.0).
The source bytes are identical. The upstream copyright and MIT permission text
are included in the dependency notices, with the pinned source URL and matching
SHA-256 values in `source-verification.json`.

This records a current license grant for the identical source. It does not
claim that the newer LICENSE file was present in the historical 1.0.6 package.
No runtime dependency, lockfile, or application code changed during this review.

## Distribution scope

The source repository includes dependency notices and brand provenance files.
Container images, operating-system packages, operator-supplied model accounts,
and separately licensed geolocation databases retain their own terms.
