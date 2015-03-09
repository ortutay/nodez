$(document).init(function() {
  var wire = new WebSocket('ws://' + location.host + '/nodez/wire/stream');
  var info = new WebSocket('ws://' + location.host + '/nodez/info/stream');

  wire.onmessage = handleWireStreamMessage
  info.onmessage = handleInfoStreamMessage
});


function handleWireStreamMessage(e) {
  d = $.parseJSON(e.data)

  console.log(d)

	datestr = "3 seconds ago";

  switch (d.command) {
  case "sync":
    return;
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
		iosBlock += "<div class='message-stream-tx-io-header'>Inputs</div>";
		iosBlock += "<div class='message-stream-tx-io-header'>Outputs</div>";
		iosBlock += "</div>";
		iosBlock += inputsOutputsFormatFn(d.tx.inputs);
		iosBlock += "<div class='message-stream-tx-io-arrow'> => </div>";
		iosBlock += inputsOutputsFormatFn(d.tx.outputs);
		iosBlock += "</div>";

		function statFormatFn(label, val) {
			block = "<div class='message-stream-tx-stat'>";
			block += "<div class='message-stream-tx-stat-label'>" + label + "</div>";
			block += "<div class='message-stream-tx-stat-val'>" + val + "</div>";
			block += "</div>";
			return block;
		}
		var statsBlock = "<div class='message-stream-tx-stats'>";
		statsBlock += "<div class='message-stream-tx-stats-header'>Details</div>";
		statsBlock += statFormatFn("Inputs", s2btc(d.tx.inputsValue));
		statsBlock += statFormatFn("Outputs", s2btc(d.tx.outputsValue));
		statsBlock += statFormatFn("Fee", s2btc(d.tx.fee));
		statsBlock += statFormatFn("Size", d.tx.bytes + " bytes");
		statsBlock += "</div>";

		el = $("#messageStream").prepend(
		"<div class='message-stream " + d.command + "'>" +
			"<div class='message-stream-left'>" +
				"<div class='message-stream-cmd'>transaction</div>" +
				"<div class='message-stream-datetime'>" + datestr + "</div>" +
			"</div>" +
			"<div class='message-stream-main'>" +
				"<div class='message-stream-tx-value'>" +
				  s2btc(d.tx.outputsValue) +
				"</div>" +
				"<div class='message-stream-hash'>" + d.tx.hash + "</div>" +
			"</div>" +
			"<div class='message-stream-tx-details'>" +
  			iosBlock +
  			statsBlock +
			"</div>" +
		"</div>");
    break;

  case "inv":
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
    el = $("#messageStream").prepend(
      "<div class='message-stream'>" + 
      "<div class='message-stream-cmd'>" + d.command + "</div>" +
      "<div class='message-stream-msg'>" + d.message + "</div>" +
      "</div>");
  }
  // Prune messages to avoid using too much memory. CSS overflow is used
  // for styling.
  var items = $("#messageStream .message-stream-item");
  for (var i = items.length; i > 500; i--) {
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
