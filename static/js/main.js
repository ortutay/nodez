$(document).init(function() {
  var wire = new WebSocket('ws://' + location.host + '/nodez/wire/stream');
  var info = new WebSocket('ws://' + location.host + '/nodez/info/stream');

  wire.onmessage = handleWireStreamMessage
  info.onmessage = handleInfoStreamMessage
});

function toggleDetail(el, html) {
	// TODO(ortutay): get fullHtml from el
	dupe = $(html);
	dupe.addClass("show-details");
	dupe.find(".close-details").click(function () {
		dupe.remove();
	})
	$("body").prepend(dupe);
	if (this.prevDupe) {
		this.prevDupe.remove();
	}
	this.prevDupe = dupe;
}

function formatStatsBlock(data) {
	var statsBlock = "<div class='message-stream-tx-stats'>";
	statsBlock += "<div class='message-stream-tx-header'>Details</div>";
	for (i = 0; i < data.length; i += 2) {
		var label = data[i];
		var val = data[i+1];
		statsBlock += "<div class='message-stream-tx-stat'>";
		statsBlock += "<div class='message-stream-tx-stat-label'>" + label + "</div>";
		statsBlock += "<div class='message-stream-tx-stat-val'>" + val + "</div>";
		statsBlock += "</div>";
	}
	return statsBlock;
}

function handleWireStreamMessage(e) {
  d = $.parseJSON(e.data)

  // console.log(d)

  switch (d.command) {
  // case "sync":
  //   return;
    // item = d.snc
    // el = $("#messageStream").prepend("<div class='message-stream-item'>" + item.hash + "</div>");

  case "tx":
		// Make sure inputs always have an "address" of some sort
		for (var i = 0; i < d.tx.inputs.length; i++) {
			var input = d.tx.inputs[i];
			if (input.address == "") {
			  input.address = input.prevOutPoint.hash + ":" + input.prevOutPoint.index;
			}
		}

		// And outputs should also have an "address" of some sort
		for (var i = 0; i < d.tx.outputs.length; i++) {
			var output = d.tx.outputs[i];
			if (output.address == "") {
			  output.address = tx.hash + ":" + i;
			}
		}

		inputsOutputsFormatFn = function (ios) {
			var block = "";
			block += "<div class='message-stream-tx-ios'>";
			for (var i = 0; i < ios.length; i++) {
				var io = ios[i];
				block += "<div class='message-stream-tx-io'>"
				block += io.address + ": ";
				block += s2btc(io.value);
				block += "</div>";
			}
			block += "</div>";
			return block;
		}
		
    var iosBlock = "<div class='message-stream-tx-ios-wrapper'>";
		iosBlock += "<div class='message-stream-tx-io-headers'>";
		iosBlock += "<div class='message-stream-tx-header'>Inputs</div>";
		iosBlock += "<div class='message-stream-tx-header'>Outputs</div>";
		iosBlock += "</div>";
		iosBlock += inputsOutputsFormatFn(d.tx.inputs);
		iosBlock += "<div class='message-stream-tx-io-arrow'> => </div>";
		iosBlock += inputsOutputsFormatFn(d.tx.outputs);
		iosBlock += "</div>";

		statsBlock = formatStatsBlock([
			"Inputs", s2btc(d.tx.inputsValue),
			"Outputs", s2btc(d.tx.outputsValue),
			"Fee", s2btc(d.tx.fee),
			"Size", d.tx.bytes + " bytes",
		])

		var fullHtml = "<div class='message-stream " + d.command + "'>" +
			"<div class='close-details'>close</div>" +
			"<div class='message-stream-main'>" +
				  d.dateStr + " " +
				  s2btc(d.tx.outputsValue) + " " +
				  d.tx.hash + " " +
			"</div>" +
			"<div class='message-stream-tx-details'>" +
  			iosBlock +
  			statsBlock +
			"</div>" +
		"</div>";
		
		var el = $(fullHtml);

		$("#messageStream").prepend(el);

		el.click(function () { toggleDetail(el, fullHtml) });
		
    break;

	case "sync":
		statsBlock = formatStatsBlock([
			"Height", d.sync.height,
		])

		var fullHtml = "<div class='message-stream " + d.command + "'>" +
			"<div class='close-details'>close</div>" +
			"<div class='message-stream-main'>" +
			  "<div class='message-stream-tx-value'>" +
				  d.dateStr + " " +
				  "Synchronizing, block #" + d.sync.height + " " +
				  d.sync.hash + " " +
				"</div>" +
			"</div>" +
			"<div class='message-stream-tx-details'>" +
			  statsBlock +
			"</div>" +
		"</div>";

		var el = $(fullHtml);

		$("#messageStream").prepend(el);

		el.click(function () { toggleDetail(el, fullHtml) });

		break;

	case "block":
		var sizeStr = "";
		if (d.block.bytes > 1000) {
			sizeStr = d.block.bytes/1000 + " KB";
		} else {
			sizeStr = d.block.bytes + " bytes";
		}
		statsBlock = formatStatsBlock([
			"Number of transactions", d.block.numTransactions,
			"Height", d.block.height,
			"Size", sizeStr,
		])

		var fullHtml = "<div class='message-stream " + d.command + "'>" +
			"<div class='close-details'>close</div>" +
			"<div class='message-stream-main'>" +
			  "<div class='message-stream-tx-value'>" +
				  d.dateStr + " " +
				  "Block #" + d.block.height + " " +
				  d.block.hash + " " +
				"</div>" +
			"</div>" +
			"<div class='message-stream-tx-details'>" +
			  statsBlock +
			"</div>" +
		"</div>";

		var el = $(fullHtml);

		$("#messageStream").prepend(el);

		el.click(function () { toggleDetail(el, fullHtml) });

		break;

  case "inv":
    return; // TODO(ortutay): handle more message types
    for (var i = 0; i < d.inv.length; i++) {
      item = d.inv[i]
      el = $("#messageStream").prepend(
        "<div class='message-stream " + d.command + "'>" + 
        "<div class='message-stream-cmd'>" + item.type + "</div>" +
        "<div class='message-stream-hash'>" +item.hash + "</div>" +
        "</div>");
    }
    break;

  case "addr":
    return; // TODO(ortutay): handle more message types
    for (var i = 0; i < d.addresses.length; i++) {
      addr = d.addresses[i]
      el = $("#messageStream").prepend(
        "<div class='message-stream'>" + 
        "<div class='message-stream-cmd'>" + d.command + "</div>" +
        "<div class='message-stream-addr'>" +
          addr.ip + ":" + addr.port +
        "</div>" +
        "</div>");
    }
    break;

  default:
    return; // TODO(ortutay): handle more message types
    el = $("#messageStream").prepend(
      "<div class='message-stream'>" + 
      "<div class='message-stream-cmd'>" + d.command + "</div>" +
      "<div class='message-stream-msg'>" + d.message + "</div>" +
      "</div>");
  }
  // Prune messages to avoid using too much memory. CSS overflow is used
  // for styling.
  var items = $("#messageStream .message-stream-item");
  for (var i = items.length; i > 200; i--) {
    items.eq(i).remove();
  }
}

function handleInfoStreamMessage(e) {
  d = $.parseJSON(e.data);

  // console.log("info: ", d);

  $("#addr").html(d.ip + ":" + d.port);
  if (d.testnet) {
    $("#net").html("testnet");
  } else {
    $("#net").html("mainnet");
  }
  $("#height").html(numberWithCommas(d.height));
  $("#difficulty").html(numberWithCommas(Math.round(d.difficulty)));
  $("#hashesPerSec").html(numberWithCommas(Math.round(d.hashesPerSec/1e9)) + " GH/sec");
  $("#connections").html(numberWithCommas(d.connections));
  $("#bytesRecv").html(numberWithCommas(d.bytesRecv) + " bytes");
  $("#bytesSent").html(numberWithCommas(d.bytesSent) + " bytes");
}

function numberWithCommas(x) {
  var parts = x.toString().split(".");
  parts[0] = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return parts.join(".");
}

function s2b(s) {
	return s/1e8;
}

function s2btc(s) {
	return s2b(s) + " BTC";
}
