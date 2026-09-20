package vulkan

import (
	"fmt"
	"math"
	"unsafe"

	vk "github.com/goki/vulkan"

	"twopointfive/assets"
	"twopointfive/render/worldgeo"
	"twopointfive/window"
)

// NewIso stands up the Vulkan backend for the isometric level viewer: the
// shared instance/device/swapchain bootstrap (same as New/NewWorld — see
// renderer.go), a fixed-size offscreen colour+depth scene target, the
// world-geometry pipeline, and the present stage that blits it to the
// swapchain. srcW/srcH is the offscreen scene's render resolution.
func NewIso(win *window.Window, srcW, srcH int, vsync, debug bool) (*Renderer, error) {
	r := &Renderer{win: win, vsync: vsync, debug: debug, iso: true}
	r.srcWidth, r.srcHeight = srcW, srcH // letterboxViewport reads these
	r.ir = &isoResources{
		w: srcW, h: srcH,
		texCache: make(map[*assets.RGBA]*worldTex),
		voxCache: make(map[*assets.VoxelModel]*voxelMesh),
	}

	steps := []func() error{
		r.createInstance,
		r.createSurface,
		r.pickPhysicalDevice,
		r.createLogicalDevice,
		r.createSwapchain,
		r.createImageViews,
		r.createRenderPass,
		r.createFramebuffers,
		r.createCommandPool,
		r.createCommandBuffers,
		r.createSyncObjects,
		r.isoCreateSamplers,
		r.isoCreateSceneRenderPass,
		r.isoCreateOffscreen,
		r.isoCreateDummyTex,
		r.isoCreateDescriptorLayouts,
		r.isoCreateTexPool,
		r.isoCreatePresentDescriptor,
		r.isoCreateRevealResources,
		r.isoCreatePipelines,
		r.isoCreateVoxelPipeline,
		func() error { return r.isoCreateOverlay(srcW, srcH) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			r.Destroy()
			return nil, err
		}
	}
	return r, nil
}

// syncIsoTextures uploads any bitmap in g (wall/flat/sprite textures + the
// sky) that isn't resident yet — same shape as worldtex.go's
// syncWorldTextures, its own texCache instead of wr's.
func (r *Renderer) syncIsoTextures(g *worldgeo.Geometry) error {
	ir := r.ir
	missing := func(t *assets.RGBA) bool { return t != nil && ir.texCache[t] == nil }

	pending := missing(g.Sky)
	if !pending {
		for _, t := range g.Textures {
			if missing(t) {
				pending = true
				break
			}
		}
	}
	if !pending {
		return nil
	}

	vk.DeviceWaitIdle(r.device)
	for i, t := range g.Textures {
		if !missing(t) {
			continue
		}
		var bright *assets.RGBA
		if i < len(g.Brightmaps) {
			bright = g.Brightmaps[i]
		}
		if err := r.uploadIsoTexture(t, bright); err != nil {
			return err
		}
	}
	if missing(g.Sky) {
		if err := r.uploadIsoTexture(g.Sky, nil); err != nil {
			return err
		}
	}
	return nil
}

// uploadIsoTexture makes t resident: a device-local RGBA8 image with a full
// mip chain and its set-0 descriptor, cached by the bitmap pointer — same
// shape as worldtex.go's uploadWorldTexture, its own dummyTex/dsl0/texPool/
// texCache instead of wr's. makeResidentImage/recordTextureUpload (used
// inside it) are generic *Renderer methods with no wr-specific state, so
// they're reused unchanged.
func (r *Renderer) uploadIsoTexture(t, bright *assets.RGBA) error {
	ir := r.ir
	img, mips, err := r.makeResidentImage(t)
	if err != nil {
		return err
	}
	var bimg litImage
	brightView := ir.dummyTex.view
	if bright != nil {
		bimg, _, err = r.makeResidentImage(bright)
		if err != nil {
			freeLitImage(r.device, img)
			return err
		}
		brightView = bimg.view
	}
	set, err := r.isoAllocTexSet(img.view, brightView)
	if err != nil {
		freeLitImage(r.device, img)
		freeLitImage(r.device, bimg)
		return err
	}
	ir.texCache[t] = &worldTex{img: img, bright: bimg, set: set, mips: mips}
	return nil
}

func (r *Renderer) isoAllocTexSet(baseView, brightView vk.ImageView) (vk.DescriptorSet, error) {
	ir := r.ir
	info := vk.DescriptorSetAllocateInfo{
		SType:              vk.StructureTypeDescriptorSetAllocateInfo,
		DescriptorPool:     ir.texPool,
		DescriptorSetCount: 1,
		PSetLayouts:        []vk.DescriptorSetLayout{ir.dsl0},
	}
	var s vk.DescriptorSet
	if res := vk.AllocateDescriptorSets(r.device, &info, &s); res != vk.Success {
		return nil, fmt.Errorf("vulkan: iso texture descriptor set (%d resident): %s", len(ir.texCache), vk.Error(res))
	}
	imgW := func(binding uint32, view vk.ImageView) vk.WriteDescriptorSet {
		return vk.WriteDescriptorSet{
			SType:           vk.StructureTypeWriteDescriptorSet,
			DstSet:          s,
			DstBinding:      binding,
			DescriptorCount: 1,
			DescriptorType:  vk.DescriptorTypeCombinedImageSampler,
			PImageInfo: []vk.DescriptorImageInfo{{
				Sampler: ir.texSampler, ImageView: view, ImageLayout: vk.ImageLayoutShaderReadOnlyOptimal,
			}},
		}
	}
	vk.UpdateDescriptorSets(r.device, 2, []vk.WriteDescriptorSet{imgW(0, baseView), imgW(1, brightView)}, 0, nil)
	return s, nil
}

// syncIsoVoxelMeshes meshes + uploads any voxel model referenced this frame
// that isn't resident yet — same shape as worldvoxel.go's syncVoxelMeshes,
// its own voxCache instead of wr's. Lazy (mesh-on-first-sight) rather than
// worldvoxel.go's WarmVoxels-style upfront batch — a possible one-time
// stutter the first time a new model type comes into view, traded for not
// needing a second, iso-specific "warm the whole pack now" entry point;
// worth revisiting if that stutter turns out to matter in practice.
func (r *Renderer) syncIsoVoxelMeshes(g *worldgeo.Geometry) error {
	ir := r.ir
	pending := false
	for i := range g.Voxels {
		if m := g.Voxels[i].Model; m != nil && ir.voxCache[m] == nil {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	vk.DeviceWaitIdle(r.device)
	for i := range g.Voxels {
		m := g.Voxels[i].Model
		if m == nil || ir.voxCache[m] != nil {
			continue
		}
		if err := r.uploadIsoVoxelMesh(m); err != nil {
			return err
		}
	}
	return nil
}

// uploadIsoVoxelMesh mirrors worldvoxel.go's uploadVoxelMesh: mesh via the
// shared worldgeo.BuildVoxelMesh, upload via the shared
// deviceLocalVertexBuffer/wHostBuffer/submitOneShot helpers, cache in
// ir.voxCache instead of wr's.
func (r *Renderer) uploadIsoVoxelMesh(m *assets.VoxelModel) error {
	ir := r.ir
	data := worldgeo.BuildVoxelMesh(m)
	if len(data) == 0 {
		ir.voxCache[m] = &voxelMesh{} // meshed to nothing — cache the miss
		return nil
	}

	stage, stageMem, mapped, err := r.wHostBuffer(len(data), vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		return err
	}
	vk.Memcopy(mapped, data)
	defer func() {
		vk.UnmapMemory(r.device, stageMem)
		vk.DestroyBuffer(r.device, stage, nil)
		vk.FreeMemory(r.device, stageMem, nil)
	}()

	vbuf, vmem, err := r.deviceLocalVertexBuffer(len(data))
	if err != nil {
		return err
	}
	if err := r.submitOneShot(func(cmd vk.CommandBuffer) {
		vk.CmdCopyBuffer(cmd, stage, vbuf, 1, []vk.BufferCopy{{Size: vk.DeviceSize(len(data))}})
	}); err != nil {
		vk.DestroyBuffer(r.device, vbuf, nil)
		vk.FreeMemory(r.device, vmem, nil)
		return err
	}
	ir.voxCache[m] = &voxelMesh{vbuf: vbuf, vbufMem: vmem, count: uint32(len(data) / worldgeo.VoxelPackedVertexSize)}
	return nil
}

// recordIsoVoxels appends this frame's voxel-model draws inside the
// (already begun) scene render pass — mirrors worldvoxel.go's recordVoxels,
// but the push constant's fragment half is just (light, fullbright): no
// per-instance shade.zw payload, since isovoxel.frag has no dynamic lights
// to gate.
// revealAlpha > 0 makes this the occluded-silhouette pass: bind
// pipeVoxelReveal (GREATER depth) and hand isovoxel.frag the alpha in
// shade.z so a hidden voxel model shows as a flat tint.
func (r *Renderer) recordIsoVoxels(cmd vk.CommandBuffer, g *worldgeo.Geometry, vp [16]float32, revealAlpha float32) {
	ir := r.ir
	pipe := ir.pipeVoxel
	if revealAlpha > 0 {
		pipe = ir.pipeVoxelReveal
	}
	if pipe == nil || len(g.Voxels) == 0 {
		return
	}
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, pipe)

	// On the reveal pass, a model far from screen centre (i.e. far from the
	// player) doesn't get a silhouette — mirrors iso.frag's per-fragment
	// screen-radius gate for sprites. r² of the cutoff, in pixels.
	revealR2 := float64(IsoRevealScreenRadius) * float64(IsoRevealScreenRadius)
	halfW, halfH := float64(ir.w)*0.5, float64(ir.h)*0.5

	var pc [voxelPushBytes / 4]float32
	// lastVM/haveLast track the model bound by the previous drawn
	// instance — see worldvoxel.go's recordVoxels, which uses the same
	// guard to skip a redundant vertex-buffer rebind when consecutive
	// instances share one model.
	var lastVM *voxelMesh
	haveLast := false
	for i := range g.Voxels {
		inst := g.Voxels[i]
		vm := ir.voxCache[inst.Model]
		if vm == nil || vm.count == 0 {
			continue
		}
		mvp := worldgeo.Mat4Mul(vp, worldgeo.VoxelModelMatrix(inst))
		if revealAlpha > 0 && mvp[15] != 0 {
			dx := float64(mvp[12]/mvp[15]) * halfW
			dy := float64(mvp[13]/mvp[15]) * halfH
			if dx*dx+dy*dy > revealR2 {
				continue
			}
		}
		copy(pc[0:16], mvp[:])
		pc[16], pc[17], pc[18], pc[19] = inst.X, inst.Y, inst.Z, inst.Scale
		pc[20], pc[21], pc[22], pc[23] = float32(math.Cos(float64(inst.Yaw))), float32(math.Sin(float64(inst.Yaw))), 0, 0
		fb := float32(0)
		if inst.FullBright {
			fb = 1
		}
		pc[24], pc[25], pc[26], pc[27] = inst.Light, fb, revealAlpha, 0

		vk.CmdPushConstants(cmd, ir.plVoxel, vk.ShaderStageFlags(vk.ShaderStageVertexBit),
			0, voxelPushVert, unsafe.Pointer(&pc[0]))
		vk.CmdPushConstants(cmd, ir.plVoxel, vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
			voxelPushVert, voxelPushFrag, unsafe.Pointer(&pc[24]))
		if !haveLast || vm != lastVM {
			vk.CmdBindVertexBuffers(cmd, 0, 1, []vk.Buffer{vm.vbuf}, []vk.DeviceSize{0})
			lastVM, haveLast = vm, true
		}
		vk.CmdDraw(cmd, vm.count, 1, 0, 0)
	}
}

// uploadIsoStatic packs g.StaticTris and copies it into the device-local
// static vertex buffer (grown/created as needed) — same shape as world.go's
// uploadStaticWorld. Runs only when the builder bumps StaticVersion.
func (r *Renderer) uploadIsoStatic(g *worldgeo.Geometry) error {
	ir := r.ir
	if g.StaticVersion == ir.staticVersion && ir.staticVBO != nil {
		return nil
	}
	ir.staticPackBuf = worldgeo.PackVerts(g.StaticTris, ir.staticPackBuf)
	n := len(ir.staticPackBuf)
	if n == 0 {
		ir.staticVersion = g.StaticVersion
		return nil
	}
	if n > ir.staticVBOCap {
		grown := ir.staticVBOCap
		if grown == 0 {
			grown = 1 << 16
		}
		for grown < n {
			grown *= 2
		}
		vk.DeviceWaitIdle(r.device)
		if ir.staticVBO != nil {
			vk.DestroyBuffer(r.device, ir.staticVBO, nil)
			vk.FreeMemory(r.device, ir.staticVBOMem, nil)
		}
		b, m, err := r.deviceLocalVertexBuffer(grown)
		if err != nil {
			ir.staticVBO, ir.staticVBOMem, ir.staticVBOCap = nil, nil, 0
			return err
		}
		ir.staticVBO, ir.staticVBOMem, ir.staticVBOCap = b, m, grown
	}
	stage, stageMem, mapped, err := r.wHostBuffer(n, vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		return err
	}
	vk.Memcopy(mapped, ir.staticPackBuf)
	defer func() {
		vk.UnmapMemory(r.device, stageMem)
		vk.DestroyBuffer(r.device, stage, nil)
		vk.FreeMemory(r.device, stageMem, nil)
	}()
	if err := r.submitOneShot(func(cmd vk.CommandBuffer) {
		vk.CmdCopyBuffer(cmd, stage, ir.staticVBO, 1, []vk.BufferCopy{{Size: vk.DeviceSize(n)}})
	}); err != nil {
		return err
	}
	ir.staticVersion = g.StaticVersion
	return nil
}

// uploadIsoDynamic packs g.Tris (the sprite billboards AddSprites just
// built) and copies it into the device-local dynamic vertex buffer. Unlike
// the FPS hardware path this isn't on a per-frame hot path — the viewer
// only calls this again when the camera's azimuth changes enough to flip a
// sprite rotation frame (see cmd/isoengine) — so a blocking one-shot copy
// (like uploadIsoStatic's) is fine here too, rather than a host-visible
// per-frame streaming buffer.
func (r *Renderer) uploadIsoDynamic(g *worldgeo.Geometry) error {
	ir := r.ir
	ir.packBuf = worldgeo.PackVerts(g.Tris, ir.packBuf)
	n := len(ir.packBuf)
	if n == 0 {
		return nil
	}
	if n > ir.dynVBOCap {
		grown := ir.dynVBOCap
		if grown == 0 {
			grown = 1 << 14
		}
		for grown < n {
			grown *= 2
		}
		vk.DeviceWaitIdle(r.device)
		if ir.dynVBO != nil {
			vk.DestroyBuffer(r.device, ir.dynVBO, nil)
			vk.FreeMemory(r.device, ir.dynVBOMem, nil)
		}
		b, m, err := r.deviceLocalVertexBuffer(grown)
		if err != nil {
			ir.dynVBO, ir.dynVBOMem, ir.dynVBOCap = nil, nil, 0
			return err
		}
		ir.dynVBO, ir.dynVBOMem, ir.dynVBOCap = b, m, grown
	}
	stage, stageMem, mapped, err := r.wHostBuffer(n, vk.BufferUsageFlags(vk.BufferUsageTransferSrcBit))
	if err != nil {
		return err
	}
	vk.Memcopy(mapped, ir.packBuf)
	defer func() {
		vk.UnmapMemory(r.device, stageMem)
		vk.DestroyBuffer(r.device, stage, nil)
		vk.FreeMemory(r.device, stageMem, nil)
	}()
	return r.submitOneShot(func(cmd vk.CommandBuffer) {
		vk.CmdCopyBuffer(cmd, stage, ir.dynVBO, 1, []vk.BufferCopy{{Size: vk.DeviceSize(n)}})
	})
}

// IsoSun/IsoAmbient are DrawIso's fixed shading terms — a directional light
// for shape plus an ambient floor (see iso.frag). Not camera-dependent, so
// callers needing different values can just build their own isoPushConstants
// equivalent; DrawIso itself keeps a small fixed default.
var (
	IsoSunDir      = [3]float32{0.35, -0.55, 0.75} // toward the sun, normalized-ish (need not be exact — the shader doesn't renormalize... )
	IsoSunStrength = float32(0.5)
	IsoAmbient     = float32(0.35)
	// IsoPlayerFadeRadius is how far (in the offscreen render target's own
	// pixels — see iso.frag's playerFade()) a wall/masked fragment can sit
	// from screen centre and still be dithered-faded so a near wall doesn't
	// hide where the PLAYER is.
	IsoPlayerFadeRadius = float32(64)
	// IsoRevealHaloRadius is the much tighter version of the above around
	// each nearby monster/item in the reveal list — just a soft shimmer so
	// the wall says "something's here"; the silhouette pass makes it legible.
	IsoRevealHaloRadius = float32(40)
	// IsoRevealAlpha drives the occluded-silhouette pass (isoworld.go's
	// recordIsoCommandBuffer, iso.frag / isovoxel.frag ISO_REVEAL_TINT): a
	// monster/item/barrel a wall or a higher floor is hiding, WITHIN
	// IsoRevealScreenRadius px of screen centre (i.e. near the player), is
	// re-drawn as a flat gold ghost at this alpha, fading over the outer
	// third. 0 disables the pass.
	IsoRevealAlpha        = float32(0.42)
	IsoRevealScreenRadius = float32(260)
)

// uploadIsoOverlay memcpys the HUD's CPU-drawn RGBA8 plane into this frame's
// slot's mapped staging buffer — mirrors texture.go's uploadFrame exactly
// (just a memcpy; the actual buffer -> image copy is recorded into the
// command buffer, in recordIsoCommandBuffer, alongside its layout
// transitions). Empty overlay is fine (a nil/zero-length slice — an
// isometric front end that hasn't drawn a HUD this frame — and just skips
// the memcpy, leaving the image showing whatever it had before).
func (r *Renderer) uploadIsoOverlay(pix []byte) error {
	ir := r.ir
	if len(pix) == 0 {
		return nil
	}
	want := ir.overlayW * ir.overlayH * 4
	if len(pix) != want {
		return fmt.Errorf("vulkan: iso overlay is %d bytes, expected %d (%dx%d RGBA8)", len(pix), want, ir.overlayW, ir.overlayH)
	}
	vk.Memcopy(ir.overlayStagingMapped[r.currentFrame], pix)
	return nil
}

// DrawIso uploads any new textures/geometry, then records and presents one
// frame: the offscreen world-geometry pass (walls/flats/sprites, depth-
// tested, orthographic), then the present pass blitting the finished scene
// to the swapchain — structurally the tail half of DrawWorld's
// recordWorldCommandBuffer plus DrawFrame's acquire/submit/present dance,
// simplified to this viewer's single render pass and no bloom/ambient/voxel
// passes.
// reveal is the world position of every monster/item/barrel/other thing a
// wall should fade for if one stands between it and the camera — the same
// occluder-fade the player already gets (see iso.frag's playerFade/
// occluderFade), generalised. Only the first maxIsoReveal are used;
// cmd/isoengine passes whatever it already collected for CollectIsoWorldObjects.
func (r *Renderer) DrawIso(g *worldgeo.Geometry, cam worldgeo.IsoCamera, overlay []byte, reveal [][3]float32) error {
	if !r.iso {
		return fmt.Errorf("vulkan: DrawIso called on a non-iso renderer")
	}
	if err := r.syncIsoTextures(g); err != nil {
		return err
	}
	if err := r.uploadIsoStatic(g); err != nil {
		return err
	}
	if err := r.uploadIsoDynamic(g); err != nil {
		return err
	}
	if err := r.syncIsoVoxelMeshes(g); err != nil {
		return err
	}

	if w, h := r.win.FramebufferSize(); w == 0 || h == 0 {
		return nil
	}
	if r.needRecreate {
		if err := r.recreateSwapchain(); err != nil {
			if err == errSwapchainZeroExtent {
				return nil
			}
			return err
		}
		r.needRecreate = false
	}

	frame := r.currentFrame
	vk.WaitForFences(r.device, 1, []vk.Fence{r.inFlight[frame]}, vk.True, vk.MaxUint64)

	if err := r.uploadIsoOverlay(overlay); err != nil {
		return err
	}

	var imageIndex uint32
	acquireRes := vk.AcquireNextImage(r.device, r.swapchain, vk.MaxUint64, r.imageAvailable[frame], nil, &imageIndex)
	if acquireRes == vk.ErrorOutOfDate {
		r.needRecreate = true
		return nil
	} else if acquireRes != vk.Success && acquireRes != vk.Suboptimal {
		return fmt.Errorf("vulkan: iso acquire next image: %s", vk.Error(acquireRes))
	}
	if f := r.imagesInFlight[imageIndex]; f != nil {
		vk.WaitForFences(r.device, 1, []vk.Fence{f}, vk.True, vk.MaxUint64)
	}
	r.imagesInFlight[imageIndex] = r.inFlight[frame]
	vk.ResetFences(r.device, 1, []vk.Fence{r.inFlight[frame]})

	cmd := r.commandBuffers[imageIndex]
	vk.ResetCommandBuffer(cmd, 0)
	if err := r.recordIsoCommandBuffer(cmd, imageIndex, frame, g, cam, len(overlay) > 0, reveal); err != nil {
		return err
	}

	waitSemaphores := []vk.Semaphore{r.imageAvailable[frame]}
	signalSemaphores := []vk.Semaphore{r.renderFinished[imageIndex]}
	submitInfo := vk.SubmitInfo{
		SType:                vk.StructureTypeSubmitInfo,
		WaitSemaphoreCount:   1,
		PWaitSemaphores:      waitSemaphores,
		PWaitDstStageMask:    []vk.PipelineStageFlags{vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit)},
		CommandBufferCount:   1,
		PCommandBuffers:      []vk.CommandBuffer{cmd},
		SignalSemaphoreCount: 1,
		PSignalSemaphores:    signalSemaphores,
	}
	if res := vk.QueueSubmit(r.graphicsQueue, 1, []vk.SubmitInfo{submitInfo}, r.inFlight[frame]); res != vk.Success {
		return fmt.Errorf("vulkan: iso queue submit: %s", vk.Error(res))
	}
	presentInfo := vk.PresentInfo{
		SType:              vk.StructureTypePresentInfo,
		WaitSemaphoreCount: 1,
		PWaitSemaphores:    signalSemaphores,
		SwapchainCount:     1,
		PSwapchains:        []vk.Swapchain{r.swapchain},
		PImageIndices:      []uint32{imageIndex},
	}
	presentRes := vk.QueuePresent(r.presentQueue, &presentInfo)
	if presentRes == vk.ErrorOutOfDate || presentRes == vk.Suboptimal {
		r.needRecreate = true
	} else if presentRes != vk.Success {
		return fmt.Errorf("vulkan: iso queue present: %s", vk.Error(presentRes))
	}
	r.currentFrame = (frame + 1) % maxFramesInFlight
	return nil
}

func (r *Renderer) recordIsoCommandBuffer(cmd vk.CommandBuffer, imageIndex uint32, frame int, g *worldgeo.Geometry, cam worldgeo.IsoCamera, uploadOverlay bool, reveal [][3]float32) error {
	ir := r.ir
	beginInfo := vk.CommandBufferBeginInfo{SType: vk.StructureTypeCommandBufferBeginInfo}
	if res := vk.BeginCommandBuffer(cmd, &beginInfo); res != vk.Success {
		return fmt.Errorf("vulkan: iso begin command buffer: %s", vk.Error(res))
	}

	// (0) HUD overlay: copy this frame's freshly-uploaded staging buffer
	// into the sampled image, outside any render pass (layout transitions
	// and buffer->image copies can't happen inside one) — mirrors
	// recordCommandBuffer's own per-frame texture-upload dance.
	if uploadOverlay {
		subresource := vk.ImageSubresourceRange{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1}
		barrier(cmd, ir.overlayImg.image, subresource, ir.overlayImgLayout, vk.ImageLayoutTransferDstOptimal,
			vk.AccessFlags(vk.AccessShaderReadBit), vk.AccessFlags(vk.AccessTransferWriteBit),
			vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit))
		region := vk.BufferImageCopy{
			ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
			ImageExtent:      vk.Extent3D{Width: uint32(ir.overlayW), Height: uint32(ir.overlayH), Depth: 1},
		}
		vk.CmdCopyBufferToImage(cmd, ir.overlayStaging[frame], ir.overlayImg.image, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{region})
		barrier(cmd, ir.overlayImg.image, subresource, vk.ImageLayoutTransferDstOptimal, vk.ImageLayoutShaderReadOnlyOptimal,
			vk.AccessFlags(vk.AccessTransferWriteBit), vk.AccessFlags(vk.AccessShaderReadBit),
			vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit))
		ir.overlayImgLayout = vk.ImageLayoutShaderReadOnlyOptimal
		ir.overlayHasContent = true
	}

	// (1) world-geometry pass: offscreen colour + depth.
	clears := []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1}), vk.NewClearDepthStencil(1, 0)}
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType:           vk.StructureTypeRenderPassBeginInfo,
		RenderPass:      ir.rpScene,
		Framebuffer:     ir.fbScene,
		RenderArea:      vk.Rect2D{Extent: vk.Extent2D{Width: uint32(ir.w), Height: uint32(ir.h)}},
		ClearValueCount: 2,
		PClearValues:    clears,
	}, vk.SubpassContentsInline)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{fullViewport(ir.w, ir.h)})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{fullScissor(ir.w, ir.h)})

	vp := worldgeo.IsoViewProj(cam, ir.w, ir.h)
	pc := isoPushConstants{
		viewProj: vp,
		sunX:     IsoSunDir[0], sunY: IsoSunDir[1], sunZ: IsoSunDir[2], sunStrength: IsoSunStrength,
		ambientFloor: IsoAmbient,
		screenW:      float32(ir.w), screenH: float32(ir.h), fadeRadius: IsoPlayerFadeRadius,
		revealHaloRadius: IsoRevealHaloRadius, // occluderFade's per-thing wall shimmer (both passes)
	}
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, ir.pipeWorld)
	vk.CmdPushConstants(cmd, ir.plWorld,
		vk.ShaderStageFlags(vk.ShaderStageVertexBit)|vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
		0, uint32(unsafe.Sizeof(pc)), unsafe.Pointer(&pc))

	// Reveal UBO (set 1): project each monster/item/barrel/other occluded
	// thing to screen+depth so iso.frag's occluderFade can fade any wall
	// standing between it and the camera, the same way it always has for
	// the player (whose screen position/depth never change — see
	// IsoPlayerFadeRadius) but now for things that actually move around the
	// screen. One buffer per frame-in-flight slot (see isoCreateRevealResources)
	// so writing this frame's data can't race the GPU still reading last
	// frame's from the same memory.
	var revealUBO isoRevealUBO
	n := len(reveal)
	if n > maxIsoReveal {
		n = maxIsoReveal
	}
	revealUBO.count[0] = float32(n)
	for i := 0; i < n; i++ {
		sx, sy, cz := worldgeo.IsoProjectToScreenDepth(cam, ir.w, ir.h, float64(reveal[i][0]), float64(reveal[i][1]), float64(reveal[i][2]))
		revealUBO.points[i] = [4]float32{float32(sx), float32(sy), float32(cz), 0}
	}
	vk.Memcopy(ir.revealMapped[frame], unsafe.Slice((*byte)(unsafe.Pointer(&revealUBO)), unsafe.Sizeof(revealUBO)))
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, ir.plWorld, 1, 1, []vk.DescriptorSet{ir.revealSet[frame]}, 0, nil)

	// See drawTextureRuns (world.go) for the shared body; the isometric
	// renderer has no visibility grid, so visible is always nil here.
	drawRuns := func(buf vk.Buffer, runs []worldgeo.Draw) {
		drawTextureRuns(cmd, ir.plWorld, ir.texCache, g.Sky, buf, runs, nil)
	}
	drawRuns(ir.staticVBO, g.StaticDraws)
	// Water is its own Draw list now (see worldgeo.Geometry.WaterDraws' doc
	// comment) — the FPS renderer draws it with a dedicated refraction
	// pipeline, but the isometric view has no such pass, so this just draws
	// it through the same plain pipeline as everything else above,
	// restoring the exact look it had before water was split out.
	drawRuns(ir.staticVBO, g.WaterDraws)
	drawRuns(ir.dynVBO, g.Draws)
	// Voxel models — own pipeline, still inside the world pass so the depth
	// buffer sorts them against the level and the textured geometry (same
	// reasoning as world.go's recordVoxels call).
	r.recordIsoVoxels(cmd, g, vp, 0)

	// Occluded-silhouette pass: re-draw just the moving things (sprite
	// billboards + voxel models — never the walls) with the GREATER-depth
	// pipelines, so any monster/item/barrel a wall or a higher floor is
	// hiding still shows as a flat ISO_REVEAL_TINT ghost. depthWrite is off
	// and the depth buffer already has the whole scene, so no sorting is
	// needed. Cheap: it's the same handful of billboards/models again.
	if IsoRevealAlpha > 0 && (len(g.Draws) > 0 || len(g.Voxels) > 0) {
		pc.revealAlpha = IsoRevealAlpha
		pc.revealScreenRadius = IsoRevealScreenRadius
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, ir.pipeSpriteReveal)
		vk.CmdPushConstants(cmd, ir.plWorld,
			vk.ShaderStageFlags(vk.ShaderStageVertexBit)|vk.ShaderStageFlags(vk.ShaderStageFragmentBit),
			0, uint32(unsafe.Sizeof(pc)), unsafe.Pointer(&pc))
		drawRuns(ir.dynVBO, g.Draws)
		r.recordIsoVoxels(cmd, g, vp, IsoRevealAlpha)
	}

	vk.CmdEndRenderPass(cmd)

	// (2) present pass: blit colorImg into the swapchain, letterboxed.
	vpLB := r.letterboxViewport()
	vk.CmdBeginRenderPass(cmd, &vk.RenderPassBeginInfo{
		SType:           vk.StructureTypeRenderPassBeginInfo,
		RenderPass:      r.renderPass,
		Framebuffer:     r.framebuffers[imageIndex],
		RenderArea:      vk.Rect2D{Extent: r.swapchainExtent},
		ClearValueCount: 1,
		PClearValues:    []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1})},
	}, vk.SubpassContentsInline)
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, ir.presentPipeline)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{vpLB})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{{
		Offset: vk.Offset2D{X: int32(vpLB.X), Y: int32(vpLB.Y)},
		Extent: vk.Extent2D{Width: uint32(vpLB.Width), Height: uint32(vpLB.Height)},
	}})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, ir.presentLayout, 0, 1, []vk.DescriptorSet{ir.presentSet}, 0, nil)
	vk.CmdDraw(cmd, 3, 1, 0, 0)

	// (3) HUD overlay: alpha-blended over the scene we just blitted, same
	// viewport so it lines up with the visible (letterboxed) area.
	if ir.overlayHasContent {
		vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, ir.overlayPipeline)
		vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, ir.overlayPipeLayout, 0, 1, []vk.DescriptorSet{ir.overlaySet}, 0, nil)
		vk.CmdDraw(cmd, 3, 1, 0, 0)
	}

	vk.CmdEndRenderPass(cmd)

	if res := vk.EndCommandBuffer(cmd); res != vk.Success {
		return fmt.Errorf("vulkan: iso end command buffer: %s", vk.Error(res))
	}
	return nil
}

func (r *Renderer) destroyIsoResources() {
	ir := r.ir
	if ir == nil {
		return
	}
	dev := r.device

	if ir.presentPipeline != nil {
		vk.DestroyPipeline(dev, ir.presentPipeline, nil)
	}
	if ir.presentLayout != nil {
		vk.DestroyPipelineLayout(dev, ir.presentLayout, nil)
	}
	if ir.presentPool != nil {
		vk.DestroyDescriptorPool(dev, ir.presentPool, nil)
	}
	if ir.presentSetLayout != nil {
		vk.DestroyDescriptorSetLayout(dev, ir.presentSetLayout, nil)
	}
	if ir.presentSampler != nil {
		vk.DestroySampler(dev, ir.presentSampler, nil)
	}

	if ir.overlayPipeline != nil {
		vk.DestroyPipeline(dev, ir.overlayPipeline, nil)
	}
	if ir.overlayPipeLayout != nil {
		vk.DestroyPipelineLayout(dev, ir.overlayPipeLayout, nil)
	}
	if ir.overlayPool != nil {
		vk.DestroyDescriptorPool(dev, ir.overlayPool, nil)
	}
	if ir.overlaySetLayout != nil {
		vk.DestroyDescriptorSetLayout(dev, ir.overlaySetLayout, nil)
	}
	if ir.overlaySampler != nil {
		vk.DestroySampler(dev, ir.overlaySampler, nil)
	}
	freeLitImage(dev, ir.overlayImg)
	for i := range ir.overlayStaging {
		if ir.overlayStaging[i] != nil {
			vk.UnmapMemory(dev, ir.overlayStagingMem[i])
			vk.DestroyBuffer(dev, ir.overlayStaging[i], nil)
			vk.FreeMemory(dev, ir.overlayStagingMem[i], nil)
		}
	}

	if ir.pipeWorld != nil {
		vk.DestroyPipeline(dev, ir.pipeWorld, nil)
	}
	if ir.pipeSpriteReveal != nil {
		vk.DestroyPipeline(dev, ir.pipeSpriteReveal, nil)
	}
	if ir.plWorld != nil {
		vk.DestroyPipelineLayout(dev, ir.plWorld, nil)
	}

	for i := range ir.revealBuf {
		if ir.revealBuf[i] != nil {
			vk.UnmapMemory(dev, ir.revealMem[i])
			vk.DestroyBuffer(dev, ir.revealBuf[i], nil)
			vk.FreeMemory(dev, ir.revealMem[i], nil)
		}
	}
	if ir.revealPool != nil {
		vk.DestroyDescriptorPool(dev, ir.revealPool, nil)
	}
	if ir.revealSetLayout != nil {
		vk.DestroyDescriptorSetLayout(dev, ir.revealSetLayout, nil)
	}

	for _, vm := range ir.voxCache {
		if vm.vbuf != nil {
			vk.DestroyBuffer(dev, vm.vbuf, nil)
		}
		if vm.vbufMem != nil {
			vk.FreeMemory(dev, vm.vbufMem, nil)
		}
	}
	if ir.pipeVoxel != nil {
		vk.DestroyPipeline(dev, ir.pipeVoxel, nil)
	}
	if ir.pipeVoxelReveal != nil {
		vk.DestroyPipeline(dev, ir.pipeVoxelReveal, nil)
	}
	if ir.plVoxel != nil {
		vk.DestroyPipelineLayout(dev, ir.plVoxel, nil)
	}

	for _, wt := range ir.texCache {
		freeLitImage(dev, wt.img)
		freeLitImage(dev, wt.bright)
		// wt.set is freed implicitly by destroying texPool below.
	}
	if ir.texPool != nil {
		vk.DestroyDescriptorPool(dev, ir.texPool, nil)
	}
	if ir.dsl0 != nil {
		vk.DestroyDescriptorSetLayout(dev, ir.dsl0, nil)
	}
	freeLitImage(dev, ir.dummyTex)
	if ir.texSampler != nil {
		vk.DestroySampler(dev, ir.texSampler, nil)
	}

	if ir.staticVBO != nil {
		vk.DestroyBuffer(dev, ir.staticVBO, nil)
		vk.FreeMemory(dev, ir.staticVBOMem, nil)
	}
	if ir.dynVBO != nil {
		vk.DestroyBuffer(dev, ir.dynVBO, nil)
		vk.FreeMemory(dev, ir.dynVBOMem, nil)
	}

	if ir.fbScene != nil {
		vk.DestroyFramebuffer(dev, ir.fbScene, nil)
	}
	freeLitImage(dev, ir.colorImg)
	freeLitImage(dev, ir.depthImg)
	if ir.rpScene != nil {
		vk.DestroyRenderPass(dev, ir.rpScene, nil)
	}

	r.ir = nil
}
