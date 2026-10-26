# Synthetic USDC fixtures

These nine small crates contain only test-created data. Pixar OpenUSD 26.8 wrote them as USDC 0.8.0. `Sdf.Layer.FindOrOpen` reopened every crate successfully. The smallest crate is 678 bytes. Its single finite Vec2f has an intentional protocol-relative byte pattern. The 727-byte compressed integer crate reconstructs a synthetic absolute address that is absent from the raw crate and its delta encoding.

`generate.py` records the complete fixture source. It uses OpenUSD and native LZ4 only when a developer regenerates fixtures. The Go application and tests use the checked-in bytes. They do not install packages, launch external commands or load OpenUSD.

The three `.reps.json` and `.tokens.json` pairs contain independently decoded fixture data. Tests use them to change flags, indexes and compression without using the production decoder to create expected results.

The format references are the official [crate structures](https://github.com/PixarAnimationStudios/OpenUSD/blob/dev/pxr/usd/sdf/crateFile.h), [crate reader](https://github.com/PixarAnimationStudios/OpenUSD/blob/dev/pxr/usd/sdf/crateFile.cpp), [value types](https://github.com/PixarAnimationStudios/OpenUSD/blob/dev/pxr/usd/sdf/crateDataTypes.h), [integer coding](https://github.com/PixarAnimationStudios/OpenUSD/blob/dev/pxr/usd/sdf/integerCoding.cpp), [fast compression](https://github.com/PixarAnimationStudios/OpenUSD/blob/dev/pxr/base/tf/fastCompression.cpp) and [USDZ specification](https://openusd.org/release/spec_usdz.html).

The delivery parser supports USDC 0.8.0 and the type subset exercised here. It rejects other versions and types. Asset paths must resolve to actual PNG, JPEG or USDC files in the current ZIP. Percent syntax, textual layers, other resolver formats and nested package syntax are outside this subset.
