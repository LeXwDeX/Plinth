package modeldelivery

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Archive is a prepared ZIP of a task's READY local model files. Everything
// it contains was already isolated and cached when the task materialized; no
// provider is contacted. Prepare opens every file once so that the README can
// state exactly what is, and is not, inside before the first byte is sent.
type Archive struct {
	Filename  string
	MediaType string
	// Entries counts the files in the ZIP including README.txt; Bytes is the
	// sum of their sizes. MissingFormats lists the target formats left out.
	Entries        int
	Bytes          int64
	MissingFormats []string

	readmeName string
	readme     []byte
	files      []archiveFile
}

type archiveFile struct {
	path  string
	asset generation.Asset
	body  io.ReadCloser
	store bool
}

// Close releases every file the archive still holds open.
func (a *Archive) Close() error {
	var first error
	for index := range a.files {
		if a.files[index].body == nil {
			continue
		}
		if err := a.files[index].body.Close(); err != nil && first == nil {
			first = err
		}
		a.files[index].body = nil
	}
	return first
}

// WriteTo streams the ZIP: README.txt first, then the files. A failure after
// the first byte cannot change the response status, so the ZIP is left
// without its central directory and no program will open it as complete.
func (a *Archive) WriteTo(writer io.Writer) (int64, error) {
	counter := &countingWriter{writer: writer}
	archive := zip.NewWriter(counter)
	readme, err := archive.CreateHeader(&zip.FileHeader{Name: a.readmeName, Method: zip.Deflate})
	if err != nil {
		return counter.count, err
	}
	if _, err := readme.Write(a.readme); err != nil {
		return counter.count, err
	}
	for index := range a.files {
		file := &a.files[index]
		method := zip.Deflate
		if file.store {
			method = zip.Store
		}
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: file.path, Method: method, Modified: file.asset.CreatedAt.UTC()})
		if err != nil {
			return counter.count, err
		}
		// A file longer than the size recorded for it means the stored
		// bytes changed under us; never send more than was accounted for.
		written, err := io.Copy(entry, io.LimitReader(file.body, file.asset.Size+1))
		if err != nil {
			return counter.count, err
		}
		if written != file.asset.Size {
			return counter.count, fmt.Errorf("modeldelivery: %s 的实际大小与记录不符", file.path)
		}
		_ = file.body.Close()
		file.body = nil
	}
	return counter.count, archive.Close()
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.count += int64(n)
	return n, err
}

// planned file before opening.
type archiveItem struct {
	path     string
	asset    generation.Asset
	taskID   generation.TaskID
	formatID string // set for a target format's model file
	// bareConversion marks an OBJ that is a format-conversion result and has no
	// material library or textures to travel with it.
	bareConversion bool
	// convertedReferences lists the files a converted OBJ names but its
	// conversion never delivers (#330 Meshy Convert's "mtllib temp.mtl").
	// The README explains them with converted_obj_reference_note instead of
	// listing them as files the task did not save.
	convertedReferences []string
	// packageMembers lists, in order, the files inside a converted OBJ that
	// was delivered as a package (a ZIP). It is nil when the file is not such
	// a package or its directory could not be read.
	packageMembers []string
}

// archiveAssets lists the READY files collect would put into the ZIP, without
// opening any of them: the file of each target format, an OBJ's material
// library and textures, and the model-like files of the source Task that match
// no format. The coverage projection uses it to report the same size and entry
// bound Prepare enforces, so the ZIP action is never advertised for a package
// that can only answer 413.
func (s *Service) archiveAssets(v view) []generation.Asset {
	var assets []generation.Asset
	seen := map[generation.AssetID]bool{}
	add := func(asset generation.Asset) {
		if !ready(asset) || seen[asset.ID] {
			return
		}
		seen[asset.ID] = true
		assets = append(assets, asset)
	}
	spec := s.schema.Archive.ObjPackage
	for _, format := range s.schema.Formats {
		asset, from, found := s.formatAsset(v, format.ID)
		if !found {
			continue
		}
		add(asset)
		if format.ID != spec.FormatID {
			continue
		}
		if _, packaged := format.MediaTypeExtensions[strings.ToLower(asset.MediaType)]; packaged {
			continue
		}
		material := false
		for _, candidate := range sortedAssets(from) {
			switch {
			case !material && matchesAsset(spec.Material, candidate):
				material = true
				add(candidate)
			case matchesAsset(spec.Textures, candidate):
				add(candidate)
			}
		}
	}
	for _, asset := range v.assets {
		if seen[asset.ID] {
			continue
		}
		if asset.Role != modelRole && asset.Role != convertedRole && asset.Role != spec.Textures.Roles[0] {
			continue
		}
		add(asset)
	}
	return assets
}

// archiveFits reports whether the task has files to pack within the Schema's
// entry and byte bounds. The README adds one entry and no bytes are read.
func (s *Service) archiveFits(v view) bool {
	assets := s.archiveAssets(v)
	if len(assets) == 0 || len(assets)+1 > s.schema.Archive.MaxEntries {
		return false
	}
	var total int64
	for _, asset := range assets {
		total += asset.Size
	}
	return total <= s.schema.Archive.MaxBytes
}

// Prepare collects and opens the files of a task visible to scope.
func (s *Service) Prepare(ctx context.Context, scope generation.TaskListScope, taskID generation.TaskID) (*Archive, error) {
	s = s.inLocale(ctx)
	task, err := s.gateway.GetScoped(ctx, scope, taskID)
	if err != nil {
		return nil, err
	}
	if task.State != generation.TaskSucceeded {
		return nil, ErrEmptyArchive
	}
	state := s.analyze(ctx, task)
	if state.err != nil {
		return nil, state.err
	}
	v := state.view
	schema := s.schema.Archive

	items, dependencies, other, unresolved := s.collect(ctx, scope, v)
	if len(items)+len(dependencies)+len(other) == 0 {
		return nil, ErrEmptyArchive
	}
	all := append(append(append([]archiveItem(nil), items...), dependencies...), other...)
	var total int64
	for _, item := range all {
		total += item.asset.Size
	}
	if len(all)+1 > schema.MaxEntries || total > schema.MaxBytes {
		return nil, ErrArchiveTooLarge
	}

	result := &Archive{
		Filename:   strings.ReplaceAll(schema.FilenameTemplate, "{task_id}", sanitize(string(task.ID))),
		MediaType:  schema.MediaType,
		readmeName: schema.Readme.Filename,
	}
	unreadable := map[string]bool{}
	prepared := false
	defer func() {
		if !prepared {
			_ = result.Close()
		}
	}()
	for _, item := range all {
		content, err := s.gateway.OpenAssetScoped(ctx, scope, item.taskID, item.asset.ID)
		if err != nil {
			s.logger.Warn("打包时无法读取模型文件", "task_id", item.taskID, "asset_id", item.asset.ID, "error", err)
			unreadable[item.path] = true
			continue
		}
		result.files = append(result.files, archiveFile{path: item.path, asset: item.asset, body: content.Body, store: s.stored(item.asset.MediaType)})
	}
	if len(result.files) == 0 {
		return nil, ErrEmptyArchive
	}
	unresolved = s.flattenLoneObj(items, dependencies, result.files, unreadable, unresolved)

	included := map[string]string{}
	for _, item := range items {
		if !unreadable[item.path] {
			included[item.formatID] = item.path
		}
	}
	var missing []archiveMissing
	for _, format := range s.schema.Formats {
		if _, present := included[format.ID]; present || !state.coverage.Applicable {
			continue
		}
		result.MissingFormats = append(result.MissingFormats, format.ID)
		reason := s.missingReason(state, format.ID)
		for _, item := range items {
			if item.formatID == format.ID && unreadable[item.path] {
				reason = reasonAssetUnreadable
			}
		}
		missing = append(missing, archiveMissing{label: format.Label, reason: s.reasonText(reason)})
	}
	for _, item := range append(append([]archiveItem(nil), dependencies...), other...) {
		if unreadable[item.path] {
			missing = append(missing, archiveMissing{label: item.path, reason: s.reasonText(reasonAssetUnreadable)})
		}
	}
	result.readme = s.readme(task.ID, items, dependencies, other, unresolved, missing, unreadable)
	result.Entries = len(result.files) + 1
	for _, file := range result.files {
		result.Bytes += file.asset.Size
	}
	result.Bytes += int64(len(result.readme))
	prepared = true
	return result, nil
}

type archiveMissing struct{ label, reason string }

func (s *Service) missingReason(state analysis, format string) string {
	for _, row := range state.coverage.Formats {
		if row.ID == format && row.Reason != "" {
			return row.Reason
		}
	}
	return reasonNoRoute
}

func (s *Service) stored(mediaType string) bool {
	for _, candidate := range s.schema.Archive.StoredMediaTypes {
		if strings.EqualFold(candidate, mediaType) {
			return true
		}
	}
	return false
}

// collect decides the path of every file. A model file that travels alone
// stays at the archive root; a format only keeps its folder when it ships
// dependency files beside the model (an OBJ with its material library and
// textures). Dependencies are written under the names the OBJ and the library
// reference, so the model still loads after extraction.
func (s *Service) collect(ctx context.Context, scope generation.TaskListScope, v view) (formats, dependencies, other []archiveItem, unresolved []string) {
	schema := s.schema.Archive
	used := map[string]bool{strings.ToLower(schema.Readme.Filename): true}
	consumed := map[generation.AssetID]bool{}
	for _, format := range s.schema.Formats {
		asset, from, found := s.formatAsset(v, format.ID)
		if !found {
			continue
		}
		// A file whose media type the format maps to its own extension is a
		// self-contained package (a glTF or OBJ ZIP): it travels alone at the
		// root, and its bytes are not scanned for references to task files.
		_, packaged := format.MediaTypeExtensions[strings.ToLower(asset.MediaType)]
		directory := ""
		if format.ID == schema.ObjPackage.FormatID && !packaged && s.objPackagePresent(from) {
			directory = format.Directory
		}
		name := path.Join(directory, schema.FileBasename+s.formatExtension(format, asset.MediaType))
		name = uniquePath(used, name)
		formats = append(formats, archiveItem{path: name, asset: asset, taskID: from.ID, formatID: format.ID})
		consumed[asset.ID] = true
		if format.ID != schema.ObjPackage.FormatID {
			continue
		}
		if packaged {
			if s.convertedObject(asset, from) {
				formats[len(formats)-1].packageMembers = s.packageMembers(ctx, scope, from.ID, asset)
			}
			continue
		}
		deps, missing := s.objPackage(ctx, scope, directory, name, asset, from, used, consumed)
		if len(deps) == 0 && s.convertedObject(asset, from) {
			if len(missing) == 0 {
				formats[len(formats)-1].bareConversion = true
			} else if schema.Readme.ConvertedObjReferenceNote != "" {
				// The conversion ships no material library at all, so the
				// names it references were never the task's to save.
				formats[len(formats)-1].convertedReferences = missing
				missing = nil
			}
		}
		dependencies = append(dependencies, deps...)
		unresolved = append(unresolved, missing...)
	}
	for _, asset := range v.assets {
		if consumed[asset.ID] || (asset.Role != modelRole && asset.Role != convertedRole && asset.Role != schema.ObjPackage.Textures.Roles[0]) {
			continue
		}
		extension := s.mediaExtension(asset.MediaType, ".bin")
		base := sanitize(asset.Role)
		if asset.Variant != "" {
			base += "-" + sanitize(asset.Variant)
		} else {
			base += fmt.Sprintf("-%d", asset.Ordinal)
		}
		other = append(other, archiveItem{path: uniquePath(used, path.Join(schema.OtherDirectory, base+extension)), asset: asset, taskID: v.task.ID})
		consumed[asset.ID] = true
	}
	return formats, dependencies, other, unresolved
}

// objPackagePresent reports whether the task saved a material library or a
// texture for its OBJ. Only such an OBJ needs its own folder; without them the
// model file is the whole package and belongs at the archive root. The check
// uses the same readiness rule as packaging, so a row the task never stored
// cannot leave a lone model inside a folder.
func (s *Service) objPackagePresent(from generation.Task) bool {
	spec := s.schema.Archive.ObjPackage
	for _, asset := range sortedAssets(from) {
		if matchesAsset(spec.Material, asset) || matchesAsset(spec.Textures, asset) {
			return true
		}
	}
	return false
}

// flattenLoneObj moves the OBJ out of its format folder when none of the files
// it was meant to ship with could be read. Prepare opens files after collect
// fixed the paths, so an unreadable material library can otherwise leave the
// model alone in its folder; a lone model file belongs at the archive root.
// The dependency paths the README prints become root-relative as well.
func (s *Service) flattenLoneObj(items []archiveItem, dependencies []archiveItem, files []archiveFile, unreadable map[string]bool, unresolved []string) []string {
	schema := s.schema.Archive
	directory := ""
	for _, format := range s.schema.Formats {
		if format.ID == schema.ObjPackage.FormatID {
			directory = format.Directory
		}
	}
	if directory == "" {
		return unresolved
	}
	prefix := directory + "/"
	for index := range items {
		item := &items[index]
		if item.formatID != schema.ObjPackage.FormatID || unreadable[item.path] || !strings.HasPrefix(item.path, prefix) {
			continue
		}
		packed := false
		for _, file := range files {
			if file.path != item.path && strings.HasPrefix(file.path, prefix) {
				packed = true
				break
			}
		}
		if packed {
			continue
		}
		used := map[string]bool{strings.ToLower(schema.Readme.Filename): true}
		for _, file := range files {
			used[strings.ToLower(file.path)] = true
		}
		oldPath := item.path
		item.path = uniquePath(used, path.Base(oldPath))
		for fileIndex := range files {
			if files[fileIndex].path == oldPath {
				files[fileIndex].path = item.path
			}
		}
		for dependencyIndex := range dependencies {
			dependency := &dependencies[dependencyIndex]
			if !strings.HasPrefix(dependency.path, prefix) {
				continue
			}
			previous := dependency.path
			dependency.path = strings.TrimPrefix(previous, prefix)
			if unreadable[previous] {
				delete(unreadable, previous)
				unreadable[dependency.path] = true
			}
		}
		for referenceIndex := range unresolved {
			unresolved[referenceIndex] = strings.TrimPrefix(unresolved[referenceIndex], prefix)
		}
		break
	}
	return unresolved
}

// packageMembers reads the file names a package ZIP holds from its central
// directory, without extracting anything. Directories are skipped; a name
// that is not a safe relative path is kept as "" so it is counted but never
// printed. A store that cannot read at offsets
// or a damaged directory yields nil: the README then makes no claim about it.
func (s *Service) packageMembers(ctx context.Context, scope generation.TaskListScope, taskID generation.TaskID, asset generation.Asset) []string {
	content, err := s.gateway.OpenAssetScoped(ctx, scope, taskID, asset.ID)
	if err != nil {
		return nil
	}
	defer content.Body.Close()
	at, ok := content.Body.(io.ReaderAt)
	if !ok || content.Size <= 0 {
		return nil
	}
	reader, err := zip.NewReader(at, content.Size)
	if err != nil {
		return nil
	}
	members := []string{}
	for _, file := range reader.File {
		if strings.HasSuffix(file.Name, "/") {
			continue
		}
		// An unsafe or unprintable name is counted but not printed: README
		// text must stay a list of relative names, never an address.
		name, safe := relativeName(file.Name)
		if !safe || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			name = ""
		}
		members = append(members, name)
	}
	return members
}

// convertedObject reports whether the OBJ is the output of a provider's format
// conversion, as the Schema declares it: asset role and producing provider, or
// a producing Offering that is itself a conversion (#330 Meshy Convert, whose
// files are ordinary models).
func (s *Service) convertedObject(asset generation.Asset, from generation.Task) bool {
	spec := s.schema.Archive.ObjPackage
	if slices.Contains(spec.ConvertedOfferings, string(from.Offering.ID)) {
		return true
	}
	return slices.Contains(spec.ConvertedRoles, asset.Role) && slices.Contains(spec.ConvertedProviders, from.Offering.ProviderID)
}

func (s *Service) formatAsset(v view, format string) (generation.Asset, generation.Task, bool) {
	if asset, ok := v.own[format]; ok {
		return asset, v.task, true
	}
	if got, ok := v.obtained[format]; ok {
		return got.asset, got.task, true
	}
	return generation.Asset{}, generation.Task{}, false
}

func (s *Service) formatExtension(format catalog.ModelFormatSchema, mediaType string) string {
	if extension, ok := format.MediaTypeExtensions[strings.ToLower(mediaType)]; ok {
		return extension
	}
	if format.ExtensionFromMediaType {
		if extension, ok := s.schema.Archive.MediaTypeExtensions[strings.ToLower(mediaType)]; ok {
			return extension
		}
	}
	return format.Extension
}

func (s *Service) mediaExtension(mediaType, fallback string) string {
	if extension, ok := s.schema.Archive.MediaTypeExtensions[strings.ToLower(mediaType)]; ok {
		return extension
	}
	return fallback
}

// objPackage places the material library and textures of the task that holds
// the OBJ. unresolved lists material libraries or textures referenced by the
// existing files but not saved by the task.
func (s *Service) objPackage(
	ctx context.Context, scope generation.TaskListScope, directory, objectPath string,
	object generation.Asset, from generation.Task, used map[string]bool, consumed map[generation.AssetID]bool,
) ([]archiveItem, []string) {
	spec := s.schema.Archive.ObjPackage
	assets := sortedAssets(from)
	var material *generation.Asset
	var textures []generation.Asset
	for index := range assets {
		asset := assets[index]
		switch {
		case matchesAsset(spec.Material, asset) && material == nil:
			material = &assets[index]
		case matchesAsset(spec.Textures, asset):
			textures = append(textures, asset)
		}
	}
	var items []archiveItem
	var unresolved []string
	materialName := ""
	name := s.referencedLibrary(ctx, scope, from.ID, object, spec)
	if material != nil {
		if name == "" {
			name = strings.TrimSuffix(path.Base(objectPath), path.Ext(objectPath)) + spec.MaterialFileExtension
		}
		materialPath := uniquePath(used, path.Join(directory, name))
		items = append(items, archiveItem{path: materialPath, asset: *material, taskID: from.ID})
		consumed[material.ID] = true
		materialName = materialPath
	} else if name != "" {
		unresolved = append(unresolved, path.Join(directory, name))
	}
	references := s.textureReferences(ctx, scope, from.ID, material, spec, materialName, directory)
	assigned := map[int]bool{}
	for _, texture := range textures {
		key := texture.Variant
		if cut := strings.LastIndex(key, "."); cut >= 0 {
			key = key[cut+1:]
		}
		name := ""
		for index, reference := range references {
			if assigned[index] || !statementBinds(spec, key, reference.statement) {
				continue
			}
			name = reference.path
			assigned[index] = true
			break
		}
		if name == "" {
			name = path.Join(directory, sanitize(texture.Variant)+s.mediaExtension(texture.MediaType, ".bin"))
		}
		items = append(items, archiveItem{path: uniquePath(used, name), asset: texture, taskID: from.ID})
		consumed[texture.ID] = true
	}
	for index, reference := range references {
		if !assigned[index] {
			unresolved = append(unresolved, reference.path)
		}
	}
	return items, unresolved
}

func statementBinds(spec catalog.ModelObjPackageSchema, variantKey, statement string) bool {
	for _, binding := range spec.TextureBindings {
		if !strings.EqualFold(binding.VariantKey, variantKey) {
			continue
		}
		for _, candidate := range binding.Statements {
			if strings.EqualFold(candidate, statement) {
				return true
			}
		}
	}
	return false
}

type textureReference struct {
	statement string
	path      string
}

// referencedLibrary reads the first material library name from the OBJ.
func (s *Service) referencedLibrary(ctx context.Context, scope generation.TaskListScope, taskID generation.TaskID, object generation.Asset, spec catalog.ModelObjPackageSchema) string {
	content, err := s.gateway.OpenAssetScoped(ctx, scope, taskID, object.ID)
	if err != nil {
		return ""
	}
	defer content.Body.Close()
	scanner := newLineScanner(io.LimitReader(content.Body, spec.ReferenceScanBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		keyword, rest, found := strings.Cut(line, " ")
		if !found || !strings.EqualFold(keyword, spec.LibraryStatement) {
			continue
		}
		if name, ok := relativeName(strings.TrimSpace(rest)); ok {
			return name
		}
	}
	return ""
}

// textureReferences reads the texture file names a material library uses, in
// order, with the statement that references each. Names are placed relative to
// the library's folder, the way a viewer resolves them.
func (s *Service) textureReferences(
	ctx context.Context, scope generation.TaskListScope, taskID generation.TaskID,
	material *generation.Asset, spec catalog.ModelObjPackageSchema, materialPath, directory string,
) []textureReference {
	if material == nil {
		return nil
	}
	content, err := s.gateway.OpenAssetScoped(ctx, scope, taskID, material.ID)
	if err != nil {
		return nil
	}
	defer content.Body.Close()
	base := path.Dir(materialPath)
	if base == "." || base == "" {
		base = directory
	}
	var references []textureReference
	seen := map[string]bool{}
	scanner := newLineScanner(io.LimitReader(content.Body, spec.ReferenceScanBytes))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		statement := strings.ToLower(fields[0])
		if !strings.HasPrefix(statement, "map_") && statement != "bump" && statement != "norm" && statement != "disp" && statement != "decal" && statement != "refl" {
			continue
		}
		name, ok := relativeName(fields[len(fields)-1])
		if !ok {
			continue
		}
		full := path.Join(base, name)
		if seen[full] {
			continue
		}
		seen[full] = true
		references = append(references, textureReference{statement: statement, path: full})
	}
	return references
}

func newLineScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return scanner
}

// relativeName accepts only a name that stays inside the folder it is
// extracted to.
func relativeName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.Trim(name, "\"")
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func sanitize(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	result := strings.TrimLeft(builder.String(), ".")
	if len(result) > 80 {
		result = result[:80]
	}
	if result == "" {
		return "asset"
	}
	return result
}

// uniquePath returns a path not used yet and records it. Comparison ignores
// case because archives are routinely extracted on case-insensitive systems.
func uniquePath(used map[string]bool, name string) string {
	candidate := name
	extension := path.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for counter := 2; used[strings.ToLower(candidate)]; counter++ {
		candidate = fmt.Sprintf("%s-%d%s", stem, counter, extension)
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

func (s *Service) readme(
	taskID generation.TaskID, items, dependencies, other []archiveItem,
	unresolved []string, missing []archiveMissing, unreadable map[string]bool,
) []byte {
	text := s.schema.Archive.Readme
	var builder strings.Builder
	line := func(value string) { builder.WriteString(value + "\n") }
	line(text.Title)
	line(strings.ReplaceAll(text.TaskLine, "{task_id}", string(taskID)))
	line("")
	line(text.IncludedHeading)
	count := 0
	labels := map[string]string{}
	for _, format := range s.schema.Formats {
		labels[format.ID] = format.Label
	}
	for _, item := range items {
		if unreadable[item.path] {
			continue
		}
		replacer := strings.NewReplacer("{label}", labels[item.formatID], "{path}", item.path)
		line(replacer.Replace(text.IncludedLine))
		count++
	}
	if count == 0 {
		line(text.NoneIncluded)
	}
	dependencyLines := []string{}
	for _, item := range dependencies {
		if !unreadable[item.path] {
			dependencyLines = append(dependencyLines, strings.ReplaceAll(text.DependencyLine, "{path}", item.path))
		}
	}
	for _, name := range unresolved {
		dependencyLines = append(dependencyLines, strings.ReplaceAll(text.DependencyMissingLine, "{path}", name))
	}
	if len(dependencyLines) > 0 {
		line("")
		line(text.DependencyHeading)
		for _, entry := range dependencyLines {
			line(entry)
		}
	}
	otherLines := []string{}
	for _, item := range other {
		if !unreadable[item.path] {
			otherLines = append(otherLines, strings.ReplaceAll(text.OtherLine, "{path}", item.path))
		}
	}
	if text.ConvertedObjNote != "" {
		for _, item := range items {
			if item.bareConversion && !unreadable[item.path] {
				line("")
				line(strings.ReplaceAll(text.ConvertedObjNote, "{path}", item.path))
			}
		}
	}
	if text.ConvertedObjReferenceNote != "" {
		for _, item := range items {
			if len(item.convertedReferences) > 0 && !unreadable[item.path] {
				line("")
				line(strings.NewReplacer("{path}", item.path, "{references}", strings.Join(item.convertedReferences, ", ")).Replace(text.ConvertedObjReferenceNote))
			}
		}
	}
	if text.ConvertedObjPackageNote != "" {
		for _, item := range items {
			if item.packageMembers == nil || unreadable[item.path] {
				continue
			}
			line("")
			line(strings.NewReplacer("{path}", item.path, "{count}", strconv.Itoa(len(item.packageMembers))).Replace(text.ConvertedObjPackageNote))
			// The count above stays exact; the listing is bounded and leaves
			// out names that are not safe relative paths.
			listed, omitted := 0, false
			for _, member := range item.packageMembers {
				if member == "" || listed == s.schema.Archive.MaxEntries {
					omitted = true
					continue
				}
				line(strings.ReplaceAll(text.DependencyLine, "{path}", member))
				listed++
			}
			if omitted {
				line(strings.ReplaceAll(text.DependencyLine, "{path}", "..."))
			}
		}
	}
	if len(otherLines) > 0 {
		line("")
		line(text.OtherHeading)
		for _, entry := range otherLines {
			line(entry)
		}
	}
	if len(missing) > 0 {
		line("")
		line(text.MissingHeading)
		for _, entry := range missing {
			line(strings.NewReplacer("{label}", entry.label, "{reason}", entry.reason).Replace(text.MissingLine))
		}
		line("")
		line(text.IncompleteNote)
	} else {
		line("")
		line(text.CompleteNote)
	}
	return []byte(builder.String())
}
