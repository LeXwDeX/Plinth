package assetlist

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelcheck"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery/objpackage"
)

// The batch delivery of the acceptance board (#329c): one ZIP of the list's
// accepted items, each in its own directory, with a machine-readable manifest
// and a README at the root. Only stored result files are packed; nothing is
// generated and no provider is contacted. The bytes come from the same
// owner-scoped read port the single-task archive uses.
var (
	// ErrArchiveEmpty reports that no accepted item has a stored result file.
	// It is a business answer, not a server failure: the person sees the
	// Schema copy asking them to accept items first.
	ErrArchiveEmpty = errors.New("assetlist: no accepted item has stored files")
	// ErrArchiveTooLarge reports that the accepted files exceed the declared
	// entry or byte bound. The transport answers 413 and points at item_ids.
	ErrArchiveTooLarge = errors.New("assetlist: archive exceeds the declared bounds")
	// ErrArchiveUnavailable reports a deployment that declares no archive
	// section or wires no asset reader. The route stays unregistered then.
	ErrArchiveUnavailable = errors.New("assetlist: batch archive is not declared")
)

// ArchiveAssets opens one stored result file under an owner scope. The
// generation module implements it with OpenAssetScoped.
type ArchiveAssets interface {
	OpenAssetScoped(ctx context.Context, scope generation.TaskListScope,
		taskID generation.TaskID, assetID generation.AssetID) (generation.AssetContent, error)
}

// Archive is a prepared ZIP of a list's accepted items. Prepare has already
// opened every packed file and enforced the declared bounds, so a 413 is a
// precheck answer and never a truncated download. WriteTo streams the files
// without buffering them.
type Archive struct {
	Filename  string
	MediaType string
	// Entries counts every ZIP entry including the manifest and the README;
	// Bytes is the sum of their unpacked sizes.
	Entries int
	Bytes   int64
	// ItemCount counts the accepted items with at least one packed file.
	ItemCount int

	createdAt    time.Time
	manifestName string
	readmeName   string
	manifest     []byte
	readme       []byte
	files        []*archiveFile
}

type archiveFile struct {
	path      string
	mediaType string
	size      int64
	createdAt time.Time
	stored    bool
	body      io.ReadCloser
	source    archiveSource
}

// Close releases every file the archive still holds open.
func (archive *Archive) Close() error {
	var first error
	for _, file := range archive.files {
		if file.body == nil {
			continue
		}
		if err := file.body.Close(); err != nil && first == nil {
			first = err
		}
		file.body = nil
	}
	return first
}

// WriteTo streams the ZIP: the manifest, the README, then every packed file.
// A failure after the first byte cannot change the response status; the ZIP is
// then left without its central directory and no program opens it as complete.
func (archive *Archive) WriteTo(writer io.Writer) (int64, error) {
	counter := &archiveCountingWriter{writer: writer}
	zipWriter := zip.NewWriter(counter)
	for _, root := range []struct {
		name string
		body []byte
	}{{archive.manifestName, archive.manifest}, {archive.readmeName, archive.readme}} {
		entry, err := zipWriter.CreateHeader(&zip.FileHeader{
			Name: root.name, Method: zip.Deflate, Modified: archive.createdAt,
		})
		if err != nil {
			return counter.count, err
		}
		if _, err := entry.Write(root.body); err != nil {
			return counter.count, err
		}
	}
	for _, file := range archive.files {
		method := zip.Deflate
		if file.stored {
			method = zip.Store
		}
		entry, err := zipWriter.CreateHeader(&zip.FileHeader{
			Name: file.path, Method: method, Modified: file.createdAt,
		})
		if err != nil {
			return counter.count, err
		}
		// Never send more than the size the precheck accounted for.
		written, err := io.Copy(entry, io.LimitReader(file.body, file.size+1))
		if err != nil {
			return counter.count, err
		}
		if written != file.size {
			return counter.count, fmt.Errorf("assetlist: %s 的实际大小与记录不符", file.path)
		}
		_ = file.body.Close()
		file.body = nil
	}
	return counter.count, zipWriter.Close()
}

type archiveCountingWriter struct {
	writer io.Writer
	count  int64
}

func (writer *archiveCountingWriter) Write(data []byte) (int, error) {
	n, err := writer.writer.Write(data)
	writer.count += int64(n)
	return n, err
}

// ArchiveManifest is the root manifest.json: the list identity, the time it
// was packed and one entry per packed item. Field names are the archive's
// machine contract; the README repeats the same facts for people.
type ArchiveManifest struct {
	ListID      string                `json:"list_id"`
	Title       string                `json:"title"`
	GeneratedAt time.Time             `json:"generated_at"`
	Items       []ArchiveManifestItem `json:"items"`
}

// ArchiveManifestItem is one packed list item: its board identity, the
// acceptance snapshot's facts and the files its directory holds.
type ArchiveManifestItem struct {
	Position  int    `json:"position"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Round     int    `json:"round"`
	TaskID    string `json:"task_id,omitempty"`
	Directory string `json:"directory"`
	// Formats are the file formats its directory holds, in first-seen order.
	Formats []string `json:"formats"`
	// Triangles and TextureMaxEdge come from the acceptance snapshot's 3D
	// checks; null means the snapshot did not judge it.
	Triangles      *int64                 `json:"triangles"`
	TextureMaxEdge *int64                 `json:"texture_max_edge"`
	Checks         []ArchiveManifestCheck `json:"checks"`
	Files          []ArchiveManifestFile  `json:"files"`
	// Missing names the recorded files the store no longer had. They are
	// absent from the ZIP and from Files.
	Missing []ArchiveManifestMissing `json:"missing,omitempty"`
}

// ArchiveManifestFile is one packed file. SHA256 is the snapshot's recorded
// digest, which the delivery layer already verifies when it reads the bytes.
type ArchiveManifestFile struct {
	Path   string `json:"path"`
	Role   string `json:"role,omitempty"`
	Format string `json:"format"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// ArchiveManifestMissing is one recorded result file the store no longer has:
// the acceptance snapshot still names it, but the ZIP cannot carry it. It is a
// fact of the download, never a reason to fail it.
type ArchiveManifestMissing struct {
	AssetID   string `json:"asset_id"`
	Role      string `json:"role,omitempty"`
	Variant   string `json:"variant,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// ArchiveManifestCheck is one acceptance criterion as the board showed it.
type ArchiveManifestCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Actual any    `json:"actual,omitempty"`
	Budget string `json:"budget,omitempty"`
}

// archiveSource is one stored result file of an accepted item before it is
// opened: the snapshot's reference and recorded facts.
type archiveSource struct {
	assetID   string
	taskID    string
	role      string
	variant   string
	mediaType string
	size      int64
	sha256    string
}

// archivePlannedItem is one accepted item during preparation.
type archivePlannedItem struct {
	item           Item
	position       int
	dir            string
	taskID         string
	formats        []string
	sources        []archiveSource
	files          []*archiveFile
	missing        []archiveSource
	checks         []ArchiveManifestCheck
	triangles      *int64
	textureMaxEdge *int64
}

// PrepareArchive packs the accepted items of one owner's list. itemIDs may
// narrow the selection to part of the accepted items (the batching answer to
// a 413); an id outside the list is ErrNotFound. Items that are not accepted
// are never packed.
func (service *Service) PrepareArchive(
	ctx context.Context,
	ownerID, projectID, listID string,
	itemIDs []string,
) (*Archive, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if ownerID == "" || projectID == "" || listID == "" {
		return nil, ErrNotFound
	}
	if service.archive == nil || service.archiveAssets == nil {
		return nil, ErrArchiveUnavailable
	}
	list, err := service.repository.GetList(ctx, ownerID, projectID, listID)
	if err != nil {
		return nil, err
	}
	requested := make(map[string]bool, len(itemIDs))
	known := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		known[item.ID] = true
	}
	for _, itemID := range uniqueStrings(itemIDs) {
		if !known[itemID] {
			return nil, ErrNotFound
		}
		requested[itemID] = true
	}
	planned := make([]*archivePlannedItem, 0, len(list.Items))
	var recordedBytes int64
	var sourceCount int
	for position, item := range list.Items {
		if item.State != StateAccepted {
			continue
		}
		if len(requested) > 0 && !requested[item.ID] {
			continue
		}
		sources, taskID := archiveItemSources(item)
		if len(sources) == 0 {
			continue
		}
		planned = append(planned, &archivePlannedItem{
			item: item, position: position + 1, taskID: taskID, sources: sources,
		})
		sourceCount += len(sources)
		for _, source := range sources {
			recordedBytes += source.size
		}
	}
	if len(planned) == 0 {
		return nil, ErrArchiveEmpty
	}
	// Fast precheck on the snapshot's recorded facts: refuse before opening a
	// single file, so an oversized selection never touches storage.
	if sourceCount+2 > service.archive.MaxEntries || recordedBytes > service.archive.MaxBytes {
		return nil, ErrArchiveTooLarge
	}
	scope := generation.OwnTasks(generation.OwnerID(ownerID))
	now := service.clock()
	prepared := &Archive{
		Filename:     strings.ReplaceAll(service.archive.FilenameTemplate, "{list_id}", listID),
		MediaType:    service.archive.MediaType,
		createdAt:    now,
		manifestName: service.archive.ManifestFilename,
		readmeName:   service.archive.ReadmeFilename,
	}
	ok := false
	defer func() {
		if ok {
			return
		}
		for _, entry := range planned {
			for _, file := range entry.files {
				if file.body != nil {
					_ = file.body.Close()
					file.body = nil
				}
			}
		}
	}()
	packed := planned[:0]
	for _, entry := range planned {
		for _, source := range entry.sources {
			content, err := service.archiveAssets.OpenAssetScoped(
				ctx, scope, generation.TaskID(source.taskID), generation.AssetID(source.assetID))
			if err != nil {
				if generation.IsCode(err, generation.ErrNotFound) {
					// The snapshot still records the file, but the store no
					// longer has it: pack what is left and name the gap in the
					// manifest and README instead of failing the download.
					entry.missing = append(entry.missing, source)
					continue
				}
				// A store that cannot be read is a server failure, never a
				// quietly short ZIP: the transport answers 5xx with the
				// request id and this cause in the server log.
				return nil, fmt.Errorf("assetlist: open %s: %w", source.assetID, err)
			}
			mediaType := strings.TrimSpace(content.MediaType)
			if mediaType == "" {
				mediaType = source.mediaType
			}
			size := content.Size
			if size <= 0 {
				size = source.size
			}
			entry.files = append(entry.files, &archiveFile{
				mediaType: mediaType,
				size:      size,
				createdAt: now,
				stored:    service.archiveStored(mediaType),
				body:      content.Body,
				source:    source,
			})
			entry.formats = append(entry.formats, archiveFormat(mediaType, source.variant))
		}
		if len(entry.files) == 0 {
			continue
		}
		entry.formats = uniqueFormats(entry.formats)
		packed = append(packed, entry)
	}
	if len(packed) == 0 {
		return nil, ErrArchiveEmpty
	}
	usedDirs := make(map[string]bool, len(packed))
	manifest := ArchiveManifest{
		ListID: list.ID, Title: list.Title, GeneratedAt: now.UTC(),
		Items: make([]ArchiveManifestItem, 0, len(packed)),
	}
	var totalBytes int64
	for _, entry := range packed {
		name := uniqueArchiveName(usedDirs,
			sanitizeArchiveName(entry.item.Name, service.archive.DirectoryNameMaxBytes))
		entry.dir = fmt.Sprintf("%02d-%s", entry.position, name)
		service.planArchivePaths(ctx, scope, entry)
		facts, checks := archiveItemFacts(entry.item.Review)
		files := make([]ArchiveManifestFile, 0, len(entry.files))
		for _, file := range entry.files {
			files = append(files, ArchiveManifestFile{
				Path: file.path, Role: file.source.role, Format: archiveFormat(file.mediaType, file.source.variant),
				Size: file.size, SHA256: file.source.sha256,
			})
			totalBytes += file.size
		}
		missing := make([]ArchiveManifestMissing, 0, len(entry.missing))
		for _, source := range entry.missing {
			missing = append(missing, ArchiveManifestMissing{
				AssetID: source.assetID, Role: source.role,
				Variant: source.variant, MediaType: source.mediaType,
			})
		}
		manifest.Items = append(manifest.Items, ArchiveManifestItem{
			Position: entry.position, Name: entry.item.Name, Kind: entry.item.Kind,
			Round: entry.item.Round, TaskID: entry.taskID, Directory: entry.dir,
			Formats: entry.formats, Triangles: facts.triangles,
			TextureMaxEdge: facts.textureMaxEdge, Checks: checks, Files: files,
			Missing: missing,
		})
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("assetlist: encode archive manifest: %w", err)
	}
	prepared.manifest = append(encoded, '\n')
	for _, entry := range packed {
		prepared.files = append(prepared.files, entry.files...)
	}
	prepared.readme = service.archiveReadme(ctx, manifest, len(prepared.files))
	prepared.Entries = len(prepared.files) + 2
	prepared.ItemCount = len(packed)
	prepared.Bytes = totalBytes + int64(len(prepared.manifest)) + int64(len(prepared.readme))
	if prepared.Entries > service.archive.MaxEntries || prepared.Bytes > service.archive.MaxBytes {
		return nil, ErrArchiveTooLarge
	}
	ok = true
	return prepared, nil
}

// archiveItemSources reads the item's stored result references. The review
// snapshot is authoritative (role, variant, media type, size, digest); an item
// reported before snapshots existed falls back to result_json, whose media type
// is unknown until the file is opened.
func archiveItemSources(item Item) ([]archiveSource, string) {
	if len(item.Review) > 0 {
		snapshot := ReviewSnapshot{}
		if json.Unmarshal(item.Review, &snapshot) == nil && snapshot.TaskID != "" {
			sources := make([]archiveSource, 0, len(snapshot.Artifacts))
			for _, artifact := range snapshot.Artifacts {
				if artifact.AssetID == "" {
					continue
				}
				sources = append(sources, archiveSource{
					assetID: artifact.AssetID, taskID: snapshot.TaskID,
					role: artifact.Role, variant: artifact.Variant,
					mediaType: artifact.MediaType, size: artifact.Size, sha256: artifact.SHA256,
				})
			}
			if len(sources) > 0 {
				return sources, snapshot.TaskID
			}
		}
	}
	var result struct {
		TaskID string `json:"task_id"`
		Assets []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"assets"`
	}
	if len(item.Result) == 0 || json.Unmarshal(item.Result, &result) != nil || result.TaskID == "" {
		return nil, ""
	}
	sources := make([]archiveSource, 0, len(result.Assets))
	for _, asset := range result.Assets {
		if asset.ID == "" {
			continue
		}
		variant := ""
		if cut := strings.LastIndex(asset.Name, "/"); cut >= 0 {
			variant = asset.Name[cut+1:]
		}
		sources = append(sources, archiveSource{
			assetID: asset.ID, taskID: result.TaskID, role: asset.Role, variant: variant,
		})
	}
	return sources, result.TaskID
}

// archiveFacts is the part of the acceptance snapshot the manifest repeats.
type archiveFacts struct {
	triangles      *int64
	textureMaxEdge *int64
}

// archiveItemFacts reads the snapshot's 3D and 2D judgments: the triangle
// count and the largest texture edge are the checks' actual values, never a
// re-inspection.
func archiveItemFacts(raw json.RawMessage) (archiveFacts, []ArchiveManifestCheck) {
	facts := archiveFacts{}
	checks := []ArchiveManifestCheck{}
	if len(raw) == 0 {
		return facts, checks
	}
	snapshot := ReviewSnapshot{}
	if json.Unmarshal(raw, &snapshot) != nil {
		return facts, checks
	}
	appendChecks := func(source []ReviewCheck) {
		for _, check := range source {
			checks = append(checks, ArchiveManifestCheck{
				Check: check.Check, Status: check.Status, Actual: check.Actual, Budget: check.Budget,
			})
			value, ok := archiveNumber(check.Actual)
			if !ok {
				continue
			}
			switch check.Check {
			case modelcheck.CheckTriangles:
				facts.triangles = &value
			case modelcheck.CheckTextureSize:
				facts.textureMaxEdge = &value
			}
		}
	}
	if snapshot.Model3D != nil {
		appendChecks(snapshot.Model3D.Checks)
	}
	if snapshot.Image2D != nil {
		appendChecks(snapshot.Image2D.Checks)
	}
	return facts, checks
}

// archiveNumber converts one check's actual value (a decoded JSON number or a
// plain Go number) to an integer.
func archiveNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// planArchivePaths names every packed file inside one item directory. A 3D
// item follows the single-task archive layout: the main model at model.<ext>,
// its material library and textures in obj/ under the names the OBJ and the
// library reference (so the model still loads after extraction), everything
// else under other/. A 2D item keeps its pictures at the directory root.
func (service *Service) planArchivePaths(
	ctx context.Context,
	scope generation.TaskListScope,
	entry *archivePlannedItem,
) {
	used := make(map[string]bool, len(entry.files))
	mainModel := -1
	mainImage := -1
	for index, file := range entry.files {
		switch {
		case archiveIsModelFile(file):
			if mainModel < 0 || (file.mediaType == "model/gltf-binary" &&
				entry.files[mainModel].mediaType != "model/gltf-binary") {
				mainModel = index
			}
		case archiveIsImage(file.mediaType) && mainImage < 0:
			mainImage = index
		}
	}
	switch {
	case mainModel >= 0:
		main := entry.files[mainModel]
		if extension, packaged := archivePackagedOBJ(main.mediaType, main.source.variant); packaged {
			// A packaged OBJ (a ZIP of the OBJ, its library and textures) is
			// one file: it travels alone and its bytes are not scanned.
			main.path = archiveUniquePath(used, path.Join(entry.dir, "model"+extension))
			others := 0
			for index, file := range entry.files {
				if index == mainModel {
					continue
				}
				others++
				file.path = archiveUniquePath(used, path.Join(entry.dir, "other",
					archiveOtherName("file", file.source, file.mediaType, others)))
			}
			return
		}
		if archiveIsOBJ(main.mediaType, main.source.variant) {
			service.planOBJPaths(ctx, scope, entry, used, mainModel)
			return
		}
		main.path = archiveUniquePath(used, path.Join(entry.dir,
			"model"+archiveExtension(main.mediaType, main.source.variant)))
		others := 0
		for index, file := range entry.files {
			if index == mainModel {
				continue
			}
			others++
			base := "image"
			if archiveIsModel(file.mediaType) {
				base = "model"
			}
			file.path = archiveUniquePath(used, path.Join(entry.dir, "other",
				archiveOtherName(base, file.source, file.mediaType, others)))
		}
	case mainImage >= 0:
		images := 0
		others := 0
		for _, file := range entry.files {
			if archiveIsImage(file.mediaType) {
				images++
				extension := archiveExtension(file.mediaType, file.source.variant)
				name := "image" + extension
				if images > 1 {
					name = fmt.Sprintf("image-%d%s", images, extension)
				}
				file.path = archiveUniquePath(used, path.Join(entry.dir, name))
				continue
			}
			others++
			file.path = archiveUniquePath(used, path.Join(entry.dir, "other",
				archiveOtherName("file", file.source, file.mediaType, others)))
		}
	default:
		for index, file := range entry.files {
			file.path = archiveUniquePath(used, path.Join(entry.dir, "other",
				archiveOtherName("file", file.source, file.mediaType, index+1)))
		}
	}
}

// planOBJPaths lays out an OBJ item: the model in obj/, its material library
// and textures under the names the OBJ and the library reference (the
// model-delivery rules in objpackage), and every other file under other/.
func (service *Service) planOBJPaths(
	ctx context.Context,
	scope generation.TaskListScope,
	entry *archivePlannedItem,
	used map[string]bool,
	mainModel int,
) {
	objDir := path.Join(entry.dir, "obj")
	entry.files[mainModel].path = archiveUniquePath(used, path.Join(objDir, "model.obj"))
	spec := service.objPackage
	materialIndex := -1
	for index, file := range entry.files {
		if index != mainModel && archiveIsMaterial(file.source) {
			materialIndex = index
			break
		}
	}
	library := ""
	if strings.TrimSpace(spec.LibraryStatement) != "" {
		library = service.objLibraryName(ctx, scope, entry.files[mainModel].source)
	}
	var references []objpackage.Texture
	if materialIndex >= 0 {
		material := entry.files[materialIndex]
		extension := spec.MaterialFileExtension
		if extension == "" {
			// A Catalog without the OBJ section keeps a usable file name.
			extension = ".mtl"
		}
		name := library
		if name == "" {
			name = "model" + extension
		}
		material.path = archiveUniquePath(used, path.Join(objDir, name))
		references = service.objTextures(ctx, scope, material.source, material.path, objDir)
	}
	textures := make([]int, 0, len(entry.files))
	for index, file := range entry.files {
		if index == mainModel || index == materialIndex {
			continue
		}
		if archiveIsImage(file.mediaType) || archiveIsTexture(file.source) {
			textures = append(textures, index)
		}
	}
	variants := make([]string, len(textures))
	for index, file := range textures {
		variants[index] = objpackage.VariantKey(entry.files[file].source.variant)
	}
	chosen := objpackage.Assign(spec, variants, references)
	for index, file := range textures {
		name := ""
		if reference := chosen[index]; reference >= 0 {
			name = references[reference].Path
		}
		if name == "" {
			name = path.Join(objDir, fmt.Sprintf("texture-%d%s", index+1,
				archiveExtension(entry.files[file].mediaType, entry.files[file].source.variant)))
		}
		entry.files[file].path = archiveUniquePath(used, name)
	}
	others := 0
	for index, file := range entry.files {
		if index == mainModel || index == materialIndex {
			continue
		}
		packed := false
		for _, texture := range textures {
			if texture == index {
				packed = true
				break
			}
		}
		if packed {
			continue
		}
		others++
		file.path = archiveUniquePath(used, path.Join(entry.dir, "other",
			archiveOtherName("file", file.source, file.mediaType, others)))
	}
}

// objLibraryName reads the material library name the OBJ references. A store
// that cannot be read keeps the default name: the ZIP is still built.
func (service *Service) objLibraryName(
	ctx context.Context,
	scope generation.TaskListScope,
	source archiveSource,
) string {
	content, err := service.archiveAssets.OpenAssetScoped(
		ctx, scope, generation.TaskID(source.taskID), generation.AssetID(source.assetID))
	if err != nil {
		return ""
	}
	defer content.Body.Close()
	return objpackage.LibraryName(content.Body, service.objPackage)
}

// objTextures reads the texture references of the packed material library.
func (service *Service) objTextures(
	ctx context.Context,
	scope generation.TaskListScope,
	source archiveSource,
	materialPath, directory string,
) []objpackage.Texture {
	content, err := service.archiveAssets.OpenAssetScoped(
		ctx, scope, generation.TaskID(source.taskID), generation.AssetID(source.assetID))
	if err != nil {
		return nil
	}
	defer content.Body.Close()
	return objpackage.Textures(content.Body, service.objPackage, materialPath, directory)
}

func archiveOtherName(base string, source archiveSource, mediaType string, ordinal int) string {
	label := archiveLabel(source.role)
	if label == "" {
		label = archiveLabel(source.variant)
	}
	if label == "" {
		label = base
	}
	return fmt.Sprintf("%s-%d%s", label, ordinal, archiveExtension(mediaType, source.variant))
}

// archiveLabel sanitizes a role or variant for use inside a file name.
func archiveLabel(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return sanitizeArchiveName(raw, 24)
}

func archiveIsModel(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(mediaType), "model/")
}

// archiveIsModelFile reports whether a stored file is the item's model. A
// packaged model (a provider-native OBJ ZIP) is saved as application/zip, so
// only its role says what it is.
func archiveIsModelFile(file *archiveFile) bool {
	if archiveIsModel(file.mediaType) {
		return true
	}
	switch file.source.role {
	case "model", "converted_model":
		return true
	}
	return false
}

func archiveIsImage(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(mediaType), "image/")
}

func archiveIsOBJ(mediaType, variant string) bool {
	mediaType = strings.ToLower(mediaType)
	variant = strings.ToLower(strings.TrimSpace(variant))
	return variant == "obj" || variant == "obj_zip" || strings.Contains(mediaType, "obj")
}

// archivePackagedOBJ names the extension of an OBJ delivered as a package: the
// media type holds a ZIP, so the file is model.obj.zip (the same suffix the
// model delivery declares in its OBJ format's media_type_extensions) rather
// than a bare .zip or an OBJ extension the bytes do not have.
func archivePackagedOBJ(mediaType, variant string) (string, bool) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if !strings.Contains(mediaType, "zip") || !archiveIsOBJ(mediaType, variant) {
		return "", false
	}
	return ".obj.zip", true
}

func archiveIsMaterial(source archiveSource) bool {
	return source.role == "material" || source.variant == "mtl" ||
		strings.Contains(strings.ToLower(source.mediaType), "mtl")
}

func archiveIsTexture(source archiveSource) bool {
	return source.role == "texture"
}

// archiveExtension names one file by its media type. A variant only supplies
// the extension when it names a file format: a semantic slot such as primary,
// front or left is never an extension. A packaged OBJ keeps .obj.zip.
func archiveExtension(mediaType, variant string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if extension, packaged := archivePackagedOBJ(mediaType, variant); packaged {
		return extension
	}
	if extension, found := archiveMediaExtensions[mediaType]; found {
		return extension
	}
	if format := strings.ToLower(strings.TrimSpace(variant)); archiveFormatVariants[format] {
		return "." + format
	}
	if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 &&
		!archiveGenericMediaTypes[mediaType] {
		return extensions[0]
	}
	if subtype := mediaType; strings.Contains(subtype, "/") {
		subtype = subtype[strings.LastIndex(subtype, "/")+1:]
		subtype = sanitizeArchiveName(subtype, 8)
		if subtype != "item" && subtype != "octet-stream" {
			return "." + subtype
		}
	}
	return ".bin"
}

// archiveMediaExtensions maps a stored media type to its file extension. The
// generic types are deliberately absent: their bytes may be any format, so the
// saved variant decides instead.
var archiveMediaExtensions = map[string]string{
	"model/gltf-binary":  ".glb",
	"model/gltf+json":    ".gltf",
	"model/obj":          ".obj",
	"model/mtl":          ".mtl",
	"model/fbx":          ".fbx",
	"model/stl":          ".stl",
	"model/3mf":          ".3mf",
	"model/vnd.usdz+zip": ".usdz",
	"application/zip":    ".zip",
	"image/png":          ".png",
	"image/jpeg":         ".jpg",
	"image/webp":         ".webp",
	"image/gif":          ".gif",
	"image/svg+xml":      ".svg",
}

// archiveFormatVariants are the variants that name a file format. Anything
// else (primary, front, left, right, preview, ...) is a semantic slot.
var archiveFormatVariants = map[string]bool{
	"glb": true, "gltf": true, "obj": true, "mtl": true, "fbx": true,
	"stl": true, "3mf": true, "usdz": true, "zip": true,
	"png": true, "jpg": true, "jpeg": true, "webp": true, "gif": true, "svg": true,
}

// archiveGenericMediaTypes name no format; a lookup for them must not decide
// the extension.
var archiveGenericMediaTypes = map[string]bool{
	"application/octet-stream": true,
	"binary/octet-stream":      true,
}

// archiveFormat is the manifest's format id: the extension without its dot.
func archiveFormat(mediaType, variant string) string {
	return strings.TrimPrefix(archiveExtension(mediaType, variant), ".")
}

// archiveUnsafeNameCharacters are the characters Windows refuses in a file or
// directory name; they become dashes, like the path separators already did.
const archiveUnsafeNameCharacters = `:*?"<>|`

// archiveReservedDeviceNames are the Windows device names a directory may not
// use, with any extension and in any case. COM0/LPT0 are not reserved; the
// digits ¹ ² ³ are, but a name that spells them out is not.
var archiveReservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// sanitizeArchiveName makes one item name safe as a directory segment: path
// separators, the characters Windows refuses and control characters become
// dashes, a Windows device name is prefixed with an underscore, leading and
// trailing spaces and dots are dropped, and the value is truncated on a rune
// boundary. Chinese text is preserved as is.
func sanitizeArchiveName(raw string, maxBytes int) string {
	text := strings.TrimSpace(raw)
	text = strings.Map(func(character rune) rune {
		if character == '/' || character == '\\' ||
			strings.ContainsRune(archiveUnsafeNameCharacters, character) ||
			unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return '-'
		}
		return character
	}, text)
	text = strings.Trim(text, " .-")
	// truncate cuts on a rune boundary and drops the spaces and dots the cut
	// may leave at the end.
	truncate := func(value string) string {
		if maxBytes <= 0 || len(value) <= maxBytes {
			return value
		}
		value = value[:maxBytes]
		for !utf8.ValidString(value) && len(value) > 0 {
			value = value[:len(value)-1]
		}
		return strings.Trim(value, " .-")
	}
	// Windows decides a device name by the part before the first dot, ignoring
	// a trailing space there; the underscore keeps the item recognizable.
	reserved := func(value string) bool {
		stem, _, _ := strings.Cut(value, ".")
		return archiveReservedDeviceNames[strings.ToUpper(strings.Trim(stem, " ."))]
	}
	text = truncate(text)
	if reserved(text) {
		text = truncate("_" + text)
	}
	if text == "" {
		return "item"
	}
	return text
}

// uniqueArchiveName appends -2, -3, ... until the directory segment is unused.
// Comparison is case-insensitive: extraction on a case-insensitive filesystem
// must not collide either.
func uniqueArchiveName(used map[string]bool, name string) string {
	candidate := name
	for suffix := 2; used[strings.ToLower(candidate)]; suffix++ {
		candidate = fmt.Sprintf("%s-%d", name, suffix)
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

func archiveUniquePath(used map[string]bool, name string) string {
	candidate := name
	for suffix := 2; used[strings.ToLower(candidate)]; suffix++ {
		extension := path.Ext(name)
		candidate = strings.TrimSuffix(name, extension) + "-" + strconv.Itoa(suffix) + extension
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

func uniqueFormats(formats []string) []string {
	result := make([]string, 0, len(formats))
	seen := make(map[string]bool, len(formats))
	for _, format := range formats {
		if format == "" || seen[format] {
			continue
		}
		seen[format] = true
		result = append(result, format)
	}
	return result
}

// archiveStored reports whether the media type is already compressed and is
// therefore stored rather than deflated.
func (service *Service) archiveStored(mediaType string) bool {
	for _, candidate := range service.archive.StoredMediaTypes {
		if strings.EqualFold(strings.TrimSpace(candidate), mediaType) {
			return true
		}
	}
	return false
}

// archiveReadme renders the human-readable root README from the manifest, in
// the request's UI locale: every line is the Schema's own copy, so a download
// reads in the language of the browser that asked for it. The per-item block's
// indentation is layout, so it is written by code and never by the copy.
func (service *Service) archiveReadme(ctx context.Context, manifest ArchiveManifest, fileCount int) []byte {
	readme := service.archive.Readme
	locale := catalog.LocaleFrom(ctx)
	builder := &strings.Builder{}
	fill := func(text string, holders ...string) string {
		// The locale reads the template, before its holders are filled in: the
		// localizer knows the Schema's own text, never a filled copy.
		text = service.texts.Text(locale, text)
		for index := 0; index+1 < len(holders); index += 2 {
			text = strings.ReplaceAll(text, "{"+holders[index]+"}", holders[index+1])
		}
		return text
	}
	line := func(indent, text string, holders ...string) {
		builder.WriteString(indent)
		builder.WriteString(fill(text, holders...))
		builder.WriteString("\n")
	}
	line("", readme.TitleLine, "title", manifest.Title)
	line("", readme.ListLine, "list_id", manifest.ListID)
	line("", readme.GeneratedLine, "generated_at", manifest.GeneratedAt.Format(time.RFC3339))
	line("", readme.Note)
	line("", readme.SummaryLine,
		"items", strconv.Itoa(len(manifest.Items)), "files", strconv.Itoa(fileCount))
	builder.WriteString("\n")
	for _, item := range manifest.Items {
		position := fmt.Sprintf("%02d", item.Position)
		if item.Kind == "" {
			line("", readme.ItemLine, "position", position, "name", item.Name)
		} else {
			line("", readme.ItemKindLine, "position", position, "name", item.Name,
				"kind", item.Kind, "round", strconv.Itoa(item.Round))
		}
		line("    ", readme.DirectoryLine, "directory", item.Directory)
		if item.TaskID != "" {
			line("    ", readme.TaskLine, "task_id", item.TaskID)
		}
		if len(item.Formats) > 0 {
			line("    ", readme.FormatsLine, "formats",
				strings.Join(item.Formats, fill(readme.FormatSeparator)))
		}
		if item.Triangles != nil {
			line("    ", readme.TrianglesLine, "triangles", strconv.FormatInt(*item.Triangles, 10))
		}
		if item.TextureMaxEdge != nil {
			line("    ", readme.TextureLine, "texture", strconv.FormatInt(*item.TextureMaxEdge, 10))
		}
		if len(item.Checks) > 0 {
			checks := make([]string, 0, len(item.Checks))
			for _, check := range item.Checks {
				text := readme.CheckLine
				if check.Budget != "" {
					text = readme.CheckBudgetLine
					checks = append(checks, fill(text, "check", check.Check,
						"status", check.Status, "budget", check.Budget))
					continue
				}
				checks = append(checks, fill(text, "check", check.Check, "status", check.Status))
			}
			line("    ", readme.ChecksLine, "checks",
				strings.Join(checks, fill(readme.CheckSeparator)))
		}
		line("    ", readme.FilesHeading)
		for _, file := range item.Files {
			line("      ", readme.FileLine,
				"path", file.Path, "role", file.Role, "format", file.Format,
				"size", strconv.FormatInt(file.Size, 10), "sha256", file.SHA256)
		}
		if len(item.Missing) > 0 {
			// A recorded result file the store no longer has is named here:
			// the acceptance snapshot still counts it, the ZIP cannot.
			line("    ", readme.MissingHeading)
			for _, file := range item.Missing {
				line("      ", readme.MissingLine,
					"asset_id", file.AssetID, "role", service.archiveMissingRole(ctx, readme, file),
					"media_type", file.MediaType)
			}
		}
		builder.WriteString("\n")
	}
	return []byte(builder.String())
}

// archiveMissingRole names a missing file by its role or, without one, its
// variant; a file with neither gets the Schema's own word for an unnamed role.
func (service *Service) archiveMissingRole(
	ctx context.Context, readme catalog.AssetListArchiveReadmeSchema, file ArchiveManifestMissing,
) string {
	if file.Role != "" {
		return file.Role
	}
	if file.Variant != "" {
		return file.Variant
	}
	return service.texts.Text(catalog.LocaleFrom(ctx), readme.UnknownRole)
}
