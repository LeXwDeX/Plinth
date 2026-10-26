# Model-viewer compressed-format fixture

This directory contains the Khronos glTF Sample Asset `Car Concept`, from
`Models/CarConcept/glTF-KTX-BasisU-Draco` at repository revision
`edc7c9e67c639d230715049ee31f9a96a6babbbe` of
<https://github.com/KhronosGroup/glTF-Sample-Assets>.

The fixture exercises both required compression extensions:

- `KHR_draco_mesh_compression` for mesh geometry.
- `KHR_texture_basisu` for KTX2/BasisU textures.

Khronos lists the model under CC BY 4.0. Its model-specific license notice is
preserved in `LICENSE.md`, and the license text is preserved in
`CC-BY-4.0.txt`. Attribution: Khronos Group, “Car Concept,” glTF Sample
Assets. The upstream README says the source concept car was based on a public
domain model by Unity Fan. The Khronos model includes Khronos marks; this
fixture is used only in automated tests.

Upstream sources:

- Model description and compression details:
  <https://github.com/KhronosGroup/glTF-Sample-Assets/blob/edc7c9e67c639d230715049ee31f9a96a6babbbe/Models/CarConcept/README.md>
- Model license:
  <https://github.com/KhronosGroup/glTF-Sample-Assets/blob/edc7c9e67c639d230715049ee31f9a96a6babbbe/Models/CarConcept/LICENSE.md>
- CC BY 4.0:
  <https://creativecommons.org/licenses/by/4.0/legalcode>

## SHA-256

| File | SHA-256 |
| --- | --- |
| `CarConcept.data.bin` | `157d1a7ba13b1aa512db25c36cb805c95ec5a96709e9de1a5fa084dd7487658e` |
| `CarConcept.gltf` | `8bdd76afb00f6b430a0bfb4112ecf67c3c6307d27ebd8298ab92047aefca6cfd` |
| `Dash_E.ktx2` | `f9b0811444153459ca28d9211bf8cb6fbe97665071e6257b8c5b56b0edaef525` |
| `Dot_C.ktx2` | `08968e9d7d9855ae8d201cfaea889e2748fc9049a95838aa18ce689d29c041ca` |
| `Dot_N.ktx2` | `4a10cfb6134015d6897264fa7e52bc08acb41cd77b64f65726cb6716616b679e` |
| `Hex_N.ktx2` | `5b1949805e65f218f4bddd0bcce889f45d195db4f8d33eb77244524d134b9ec1` |
| `Khronos_C.ktx2` | `627acc26f726e7539967cd27f7b9ca5f84c327e5408b870243b45a49c600e62e` |
| `Mechanical_N.ktx2` | `341db23ec51698720db958677c27def788e8a7e297c7eb05d9f9677b3fbfd693` |
| `Mechanical_ORM.ktx2` | `e38d981fe93ea6d0d95137a968b141e3919938addf4f1236b1fd6fe0e8b006d6` |
| `Occlusion.ktx2` | `905008376f6701af044e11f15cbc35abad7fa92300b3be6e8c9346fa3a4926b0` |
| `Powdercoat_N.ktx2` | `518dce1ade4f1d354ffd8d804bb01a899054af909eb70fe6655372e4414d4753` |
| `Rib_N.ktx2` | `7bbd1d7776a087b48d3f7d50395d24840fd00dc5ab2622f8dce5685995df94d3` |
| `Thickness.ktx2` | `6cf5fefa01ce9817f207a72536717cc491f0f2534df236f05e92596546d20722` |
| `Tireside_C.ktx2` | `d90617659d164fd126d08061cf962aecd43b6f73990ffa3874094a4f3e022935` |
| `Tireside_N.ktx2` | `a0379569aef88f88e7b43faf17c93469affbd4bfdd9c923e30136e40b4b451c1` |
| `Tiretread_N.ktx2` | `4abedc34a3ecde5d14e232d2b3c8a59c42d55bab60ae854aaf11230dbc981f69` |

Run the full browser check with `node scripts/test-model-viewer-local-resources.mjs`.
It starts new Playwright Chromium and WebKit contexts, disables Chromium cache,
blocks non-origin requests, and verifies first-load and source-change captures.
