package vulkan

import (
	"fmt"

	vk "github.com/goki/vulkan"
)

func (r *Renderer) createRenderPass() error {
	colorAttachment := vk.AttachmentDescription{
		Format:         r.swapchainFormat,
		Samples:        vk.SampleCount1Bit,
		LoadOp:         vk.AttachmentLoadOpClear,
		StoreOp:        vk.AttachmentStoreOpStore,
		StencilLoadOp:  vk.AttachmentLoadOpDontCare,
		StencilStoreOp: vk.AttachmentStoreOpDontCare,
		InitialLayout:  vk.ImageLayoutUndefined,
		FinalLayout:    vk.ImageLayoutPresentSrc,
	}

	colorRef := vk.AttachmentReference{
		Attachment: 0,
		Layout:     vk.ImageLayoutColorAttachmentOptimal,
	}

	subpass := vk.SubpassDescription{
		PipelineBindPoint:    vk.PipelineBindPointGraphics,
		ColorAttachmentCount: 1,
		PColorAttachments:    []vk.AttachmentReference{colorRef},
	}

	dependency := vk.SubpassDependency{
		SrcSubpass:    vk.SubpassExternal,
		DstSubpass:    0,
		SrcStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
		SrcAccessMask: 0,
		DstStageMask:  vk.PipelineStageFlags(vk.PipelineStageColorAttachmentOutputBit),
		DstAccessMask: vk.AccessFlags(vk.AccessColorAttachmentWriteBit),
	}

	createInfo := vk.RenderPassCreateInfo{
		SType:           vk.StructureTypeRenderPassCreateInfo,
		AttachmentCount: 1,
		PAttachments:    []vk.AttachmentDescription{colorAttachment},
		SubpassCount:    1,
		PSubpasses:      []vk.SubpassDescription{subpass},
		DependencyCount: 1,
		PDependencies:   []vk.SubpassDependency{dependency},
	}

	var pass vk.RenderPass
	if res := vk.CreateRenderPass(r.device, &createInfo, nil, &pass); res != vk.Success {
		return fmt.Errorf("vulkan: create render pass: %s", vk.Error(res))
	}
	r.renderPass = pass
	return nil
}

func (r *Renderer) createFramebuffers() error {
	r.framebuffers = make([]vk.Framebuffer, len(r.swapchainImageViews))
	for i, view := range r.swapchainImageViews {
		createInfo := vk.FramebufferCreateInfo{
			SType:           vk.StructureTypeFramebufferCreateInfo,
			RenderPass:      r.renderPass,
			AttachmentCount: 1,
			PAttachments:    []vk.ImageView{view},
			Width:           r.swapchainExtent.Width,
			Height:          r.swapchainExtent.Height,
			Layers:          1,
		}
		var fb vk.Framebuffer
		if res := vk.CreateFramebuffer(r.device, &createInfo, nil, &fb); res != vk.Success {
			return fmt.Errorf("vulkan: create framebuffer %d: %s", i, vk.Error(res))
		}
		r.framebuffers[i] = fb
	}
	return nil
}

func (r *Renderer) createCommandPool() error {
	createInfo := vk.CommandPoolCreateInfo{
		SType:            vk.StructureTypeCommandPoolCreateInfo,
		QueueFamilyIndex: r.graphicsFamily,
		Flags:            vk.CommandPoolCreateFlags(vk.CommandPoolCreateResetCommandBufferBit),
	}
	var pool vk.CommandPool
	if res := vk.CreateCommandPool(r.device, &createInfo, nil, &pool); res != vk.Success {
		return fmt.Errorf("vulkan: create command pool: %s", vk.Error(res))
	}
	r.commandPool = pool
	return nil
}

func (r *Renderer) createCommandBuffers() error {
	r.commandBuffers = make([]vk.CommandBuffer, len(r.framebuffers))
	allocInfo := vk.CommandBufferAllocateInfo{
		SType:              vk.StructureTypeCommandBufferAllocateInfo,
		CommandPool:        r.commandPool,
		Level:              vk.CommandBufferLevelPrimary,
		CommandBufferCount: uint32(len(r.commandBuffers)),
	}
	if res := vk.AllocateCommandBuffers(r.device, &allocInfo, r.commandBuffers); res != vk.Success {
		return fmt.Errorf("vulkan: allocate command buffers: %s", vk.Error(res))
	}
	return nil
}

// createSyncObjects allocates imageAvailable/inFlight per frame-in-flight
// slot (maxFramesInFlight of each), but renderFinished per *swapchain
// image*, not per frame-in-flight slot. That asymmetry matters: a binary
// semaphore can't be re-signaled until the presentation engine is
// completely done waiting on it, which happens on its own schedule per
// image, not in lockstep with how many frames this renderer keeps in
// flight — sizing renderFinished to maxFramesInFlight (1) let a later
// submit try to re-signal a semaphore an earlier present might still be
// using, which the validation layer correctly flags
// (VUID-vkQueueSubmit-pSignalSemaphores-00067). Indexing it by imageIndex
// instead is the standard fix.
func (r *Renderer) createSyncObjects() error {
	r.imageAvailable = make([]vk.Semaphore, maxFramesInFlight)
	r.inFlight = make([]vk.Fence, maxFramesInFlight)
	r.renderFinished = make([]vk.Semaphore, len(r.swapchainImages))

	semInfo := vk.SemaphoreCreateInfo{SType: vk.StructureTypeSemaphoreCreateInfo}
	fenceInfo := vk.FenceCreateInfo{
		SType: vk.StructureTypeFenceCreateInfo,
		Flags: vk.FenceCreateFlags(vk.FenceCreateSignaledBit), // start signaled so frame 0 doesn't wait forever
	}

	for i := 0; i < maxFramesInFlight; i++ {
		if res := vk.CreateSemaphore(r.device, &semInfo, nil, &r.imageAvailable[i]); res != vk.Success {
			return fmt.Errorf("vulkan: create semaphore: %s", vk.Error(res))
		}
		if res := vk.CreateFence(r.device, &fenceInfo, nil, &r.inFlight[i]); res != vk.Success {
			return fmt.Errorf("vulkan: create fence: %s", vk.Error(res))
		}
	}
	for i := range r.renderFinished {
		if res := vk.CreateSemaphore(r.device, &semInfo, nil, &r.renderFinished[i]); res != vk.Success {
			return fmt.Errorf("vulkan: create semaphore: %s", vk.Error(res))
		}
	}
	return nil
}

// DrawFrame uploads pix (an RGBA8 raster.InternalWidth x InternalHeight
// frame) to the GPU and presents it: wait for the previous frame's
// resources to be free, copy pix into the staging buffer, acquire a
// swapchain image, record a command buffer that copies the staging buffer
// into the frame texture and draws the fullscreen blit triangle, submit,
// and present.
func (r *Renderer) DrawFrame(pix []byte) error {
	frame := r.currentFrame

	vk.WaitForFences(r.device, 1, []vk.Fence{r.inFlight[frame]}, vk.True, vk.MaxUint64)

	if err := r.uploadFrame(pix); err != nil {
		return err
	}

	var imageIndex uint32
	acquireRes := vk.AcquireNextImage(r.device, r.swapchain, vk.MaxUint64, r.imageAvailable[frame], nil, &imageIndex)
	if acquireRes == vk.ErrorOutOfDate {
		// Phase 1 doesn't yet recreate the swapchain on resize/minimize;
		// treat it as a soft failure the caller can log and continue past.
		return fmt.Errorf("vulkan: swapchain out of date (resize handling is a Phase 2 TODO)")
	} else if acquireRes != vk.Success && acquireRes != vk.Suboptimal {
		return fmt.Errorf("vulkan: acquire next image: %s", vk.Error(acquireRes))
	}

	vk.ResetFences(r.device, 1, []vk.Fence{r.inFlight[frame]})

	cmd := r.commandBuffers[imageIndex]
	vk.ResetCommandBuffer(cmd, 0)
	if err := r.recordCommandBuffer(cmd, imageIndex); err != nil {
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
		return fmt.Errorf("vulkan: queue submit: %s", vk.Error(res))
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
	if presentRes != vk.Success && presentRes != vk.Suboptimal && presentRes != vk.ErrorOutOfDate {
		return fmt.Errorf("vulkan: queue present: %s", vk.Error(presentRes))
	}

	r.currentFrame = (frame + 1) % maxFramesInFlight
	return nil
}

// letterboxViewport returns the largest centered rectangle within the
// current swapchain extent that preserves the source frame's exact
// srcWidth:srcHeight aspect ratio (320:200, i.e. 1.6:1 — see
// raster.InternalWidth/InternalHeight), letterboxing or pillarboxing with
// the render pass's black clear color rather than stretching to fill an
// arbitrary window shape. Without this, a window whose aspect ratio isn't
// exactly 1.6:1 (nearly every window — a 16:9 window is 1.778:1) visibly
// distorted the image: stretched wider than tall, which is exactly what
// made rooms look vertically compressed ("ceiling too close to the floor").
func (r *Renderer) letterboxViewport() vk.Viewport {
	srcAspect := float32(r.srcWidth) / float32(r.srcHeight)
	winW, winH := float32(r.swapchainExtent.Width), float32(r.swapchainExtent.Height)

	w, h := winW, winH
	if winW/winH > srcAspect {
		w = winH * srcAspect // window wider than source: pillarbox (bars left/right)
	} else {
		h = winW / srcAspect // window taller than source: letterbox (bars top/bottom)
	}
	return vk.Viewport{
		X: (winW - w) / 2, Y: (winH - h) / 2,
		Width: w, Height: h, MaxDepth: 1,
	}
}

// recordCommandBuffer records one frame's commands: transition the frame
// texture to a transfer destination, copy the freshly-uploaded staging
// buffer into it, transition it to shader-readable, then run the blit
// render pass (bind the pipeline + descriptor set, set the dynamic
// viewport/scissor to the window's current size, draw the 3-vertex
// fullscreen triangle).
func (r *Renderer) recordCommandBuffer(cmd vk.CommandBuffer, imageIndex uint32) error {
	beginInfo := vk.CommandBufferBeginInfo{SType: vk.StructureTypeCommandBufferBeginInfo}
	if res := vk.BeginCommandBuffer(cmd, &beginInfo); res != vk.Success {
		return fmt.Errorf("vulkan: begin command buffer: %s", vk.Error(res))
	}

	subresource := vk.ImageSubresourceRange{
		AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LevelCount: 1, LayerCount: 1,
	}

	toTransferDst := vk.ImageMemoryBarrier{
		SType:               vk.StructureTypeImageMemoryBarrier,
		OldLayout:           r.textureLayout,
		NewLayout:           vk.ImageLayoutTransferDstOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored,
		DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image:               r.textureImage,
		SubresourceRange:    subresource,
		DstAccessMask:       vk.AccessFlags(vk.AccessTransferWriteBit),
	}
	vk.CmdPipelineBarrier(cmd,
		vk.PipelineStageFlags(vk.PipelineStageTopOfPipeBit), vk.PipelineStageFlags(vk.PipelineStageTransferBit),
		0, 0, nil, 0, nil, 1, []vk.ImageMemoryBarrier{toTransferDst})

	copyRegion := vk.BufferImageCopy{
		ImageSubresource: vk.ImageSubresourceLayers{AspectMask: vk.ImageAspectFlags(vk.ImageAspectColorBit), LayerCount: 1},
		ImageExtent:      vk.Extent3D{Width: uint32(r.srcWidth), Height: uint32(r.srcHeight), Depth: 1},
	}
	vk.CmdCopyBufferToImage(cmd, r.stagingBuffer, r.textureImage, vk.ImageLayoutTransferDstOptimal, 1, []vk.BufferImageCopy{copyRegion})

	toShaderRead := vk.ImageMemoryBarrier{
		SType:               vk.StructureTypeImageMemoryBarrier,
		OldLayout:           vk.ImageLayoutTransferDstOptimal,
		NewLayout:           vk.ImageLayoutShaderReadOnlyOptimal,
		SrcQueueFamilyIndex: vk.QueueFamilyIgnored,
		DstQueueFamilyIndex: vk.QueueFamilyIgnored,
		Image:               r.textureImage,
		SubresourceRange:    subresource,
		SrcAccessMask:       vk.AccessFlags(vk.AccessTransferWriteBit),
		DstAccessMask:       vk.AccessFlags(vk.AccessShaderReadBit),
	}
	vk.CmdPipelineBarrier(cmd,
		vk.PipelineStageFlags(vk.PipelineStageTransferBit), vk.PipelineStageFlags(vk.PipelineStageFragmentShaderBit),
		0, 0, nil, 0, nil, 1, []vk.ImageMemoryBarrier{toShaderRead})
	r.textureLayout = vk.ImageLayoutShaderReadOnlyOptimal

	renderPassInfo := vk.RenderPassBeginInfo{
		SType:       vk.StructureTypeRenderPassBeginInfo,
		RenderPass:  r.renderPass,
		Framebuffer: r.framebuffers[imageIndex],
		RenderArea: vk.Rect2D{
			Offset: vk.Offset2D{X: 0, Y: 0},
			Extent: r.swapchainExtent,
		},
		ClearValueCount: 1,
		PClearValues:    []vk.ClearValue{vk.NewClearValue([]float32{0, 0, 0, 1})},
	}
	vk.CmdBeginRenderPass(cmd, &renderPassInfo, vk.SubpassContentsInline)

	vp := r.letterboxViewport()
	vk.CmdBindPipeline(cmd, vk.PipelineBindPointGraphics, r.pipeline)
	vk.CmdSetViewport(cmd, 0, 1, []vk.Viewport{vp})
	vk.CmdSetScissor(cmd, 0, 1, []vk.Rect2D{{
		Offset: vk.Offset2D{X: int32(vp.X), Y: int32(vp.Y)},
		Extent: vk.Extent2D{Width: uint32(vp.Width), Height: uint32(vp.Height)},
	}})
	vk.CmdBindDescriptorSets(cmd, vk.PipelineBindPointGraphics, r.pipelineLayout, 0, 1, []vk.DescriptorSet{r.descriptorSet}, 0, nil)
	vk.CmdDraw(cmd, 3, 1, 0, 0)

	vk.CmdEndRenderPass(cmd)

	if res := vk.EndCommandBuffer(cmd); res != vk.Success {
		return fmt.Errorf("vulkan: end command buffer: %s", vk.Error(res))
	}
	return nil
}
